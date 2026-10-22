package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ---------- avisos ----------

type Alert struct {
	Time string `json:"time"` // RFC3339
	Kind string `json:"kind"` // sostenido | cuota
	Key  string `json:"key"`  // if:eth0 | host:192.168.1.5
	Msg  string `json:"msg"`
}

func (a Alert) when() time.Time {
	t, _ := time.Parse(time.RFC3339, a.Time)
	return t
}

// minAlertGap: tiempo mínimo entre dos avisos sostenidos de la misma clave.
const minAlertGap = 30 * time.Minute

// ---------- lógica del colector (sin E/S, para poder probarla) ----------

type collector struct {
	cfg  Config
	prev struct {
		t   time.Time
		ifs map[string]IfStat
		hs  map[string]HostCounters
	}
	pending map[int64]*Bucket

	ring   map[string][]float64 // últimas tasas (Mb/s) por clave
	firing map[string]bool      // aviso sostenido en curso
	lastAt map[string]time.Time // último aviso por clave (antirrebote)

	dayStart time.Time
	dayBytes map[string]uint64 // total de hoy por equipo
	quotaHit map[string]bool
}

func newCollector(cfg Config, history []Bucket, now time.Time) *collector {
	c := &collector{
		cfg:     cfg,
		pending: map[int64]*Bucket{},
		ring:    map[string][]float64{},
		firing:  map[string]bool{},
		lastAt:  map[string]time.Time{},
	}
	c.resetDay(now, history)
	return c
}

func (c *collector) resetDay(now time.Time, history []Bucket) {
	c.dayStart = startOfDay(now)
	c.dayBytes = map[string]uint64{}
	c.quotaHit = map[string]bool{}
	for _, b := range history {
		if b.T >= c.dayStart.Unix() {
			for ip, v := range b.H {
				c.dayBytes[ip] += v[0] + v[1]
			}
		}
	}
}

func (c *collector) bucket(now time.Time) *Bucket {
	h := hourStart(now)
	b := c.pending[h]
	if b == nil {
		b = newBucket(h)
		c.pending[h] = b
	}
	return b
}

// step procesa una muestra. ifs/hs son contadores acumulados; names da el
// nombre de cada IP. Devuelve los avisos nuevos.
func (c *collector) step(now time.Time, ifs map[string]IfStat, hs map[string]HostCounters, names map[string]string) []Alert {
	prevT, prevIfs, prevHs := c.prev.t, c.prev.ifs, c.prev.hs
	c.prev.t, c.prev.ifs, c.prev.hs = now, ifs, hs
	if prevT.IsZero() {
		return nil
	}
	dt := now.Sub(prevT).Seconds()
	if dt <= 0 {
		return nil
	}
	if startOfDay(now) != c.dayStart {
		c.resetDay(now, nil)
	}

	b := c.bucket(now)
	var alerts []Alert
	mbps := func(bytes uint64) float64 { return float64(bytes) * 8 / dt / 1e6 }

	for name, cur := range ifs {
		if hiddenIface(name) {
			continue
		}
		p, ok := prevIfs[name]
		if !ok {
			continue
		}
		rx, tx := delta(cur.RX, p.RX), delta(cur.TX, p.TX)
		if rx+tx > 0 { // no se guardan ceros: ahorra espacio
			b.addIf(name, rx, tx)
		}
		if thr := c.cfg.Alerts.IfaceMbps[name]; thr > 0 {
			rate := max(mbps(rx), mbps(tx))
			if a := c.sustained(now, "if:"+name, rate, thr, "Interfaz "+name); a != nil {
				alerts = append(alerts, *a)
			}
		}
	}

	if prevHs == nil { // sin base previa (iptables acaba de volver): solo fijar base
		hs = nil
	}
	for ip, cur := range hs {
		p := prevHs[ip] // equipo nuevo: la regla acaba de crearse, base 0
		down, up := delta(cur.Down, p.Down), delta(cur.Up, p.Up)
		if down+up > 0 {
			b.addHost(ip, down, up)
			if n := names[ip]; n != "" {
				b.N[ip] = n
			}
		}
		label := ip
		if n := names[ip]; n != "" {
			label = n + " (" + ip + ")"
		}
		if thr := c.cfg.Alerts.HostMbps; thr > 0 {
			rate := max(mbps(down), mbps(up))
			if a := c.sustained(now, "host:"+ip, rate, thr, "Equipo "+label); a != nil {
				alerts = append(alerts, *a)
			}
		}
		c.dayBytes[ip] += down + up
		if q := c.cfg.Alerts.HostGBDay; q > 0 && !c.quotaHit[ip] && float64(c.dayBytes[ip]) >= q*(1<<30) {
			c.quotaHit[ip] = true
			alerts = append(alerts, Alert{
				Time: now.Format(time.RFC3339), Kind: "cuota", Key: "host:" + ip,
				Msg: fmt.Sprintf("Equipo %s ha superado %.0f GB hoy (%s)", label, q, size(c.dayBytes[ip])),
			})
		}
	}
	return alerts
}

// sustained avisa una vez cuando la tasa supera el umbral durante
// sustained_minutes seguidos, y se rearma cuando baja del umbral.
func (c *collector) sustained(now time.Time, key string, rate, thr float64, label string) *Alert {
	n := max(c.cfg.Alerts.SustainedMinutes*60/c.cfg.IntervalSec, 1)
	r := append(c.ring[key], rate)
	if len(r) > n {
		r = r[len(r)-n:]
	}
	c.ring[key] = r
	if rate < thr {
		c.firing[key] = false
		return nil
	}
	if len(r) < n || c.firing[key] {
		return nil
	}
	for _, v := range r {
		if v < thr {
			return nil
		}
	}
	c.firing[key] = true
	if t, ok := c.lastAt[key]; ok && now.Sub(t) < minAlertGap {
		return nil // evita inundar el fichero si el tráfico oscila alrededor del umbral
	}
	c.lastAt[key] = now
	return &Alert{
		Time: now.Format(time.RFC3339), Kind: "sostenido", Key: key,
		Msg: fmt.Sprintf("%s supera %.0f Mb/s durante %d min (ahora %.0f Mb/s)", label, thr, c.cfg.Alerts.SustainedMinutes, rate),
	}
}

// drain devuelve y vacía los incrementos pendientes de escribir.
func (c *collector) drain() []*Bucket {
	var out []*Bucket
	for _, b := range c.pending {
		if !b.empty() {
			out = append(out, b)
		}
	}
	c.pending = map[int64]*Bucket{}
	return out
}

// ---------- bucle del demonio ----------

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, time.Now().Format("2006-01-02 15:04:05 ")+format+"\n", a...)
}

func runCollector() error {
	cfg, cerr := loadConfig()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	if pid, ok := collectorRunning(cfg.DataDir); ok {
		return fmt.Errorf("ya hay un colector en marcha (pid %d)", pid)
	}
	os.WriteFile(pidFile(cfg.DataDir), []byte(strconv.Itoa(os.Getpid())), 0o644)
	defer os.Remove(pidFile(cfg.DataDir))
	defer collectAcct.cleanup()

	if cerr != nil {
		logf("configuración inválida, usando valores por defecto: %v", cerr)
	}
	compact := func() {
		if err := compactHistory(cfg.DataDir, time.Duration(cfg.RetentionDays)*24*time.Hour,
			int64(cfg.HistoryMaxMB)<<20, uint64(cfg.MinFreeMB)<<20, time.Now()); err != nil {
			logf("compactación: %v", err)
		}
	}
	compact()
	hist, _ := loadHistory(cfg.DataDir)
	c := newCollector(cfg, hist, time.Now())

	lowSpace := false
	flush := func() {
		pending := c.drain()
		if !hasRoom(cfg.DataDir, 64<<10, uint64(cfg.MinFreeMB)<<20) {
			if !lowSpace {
				logf("poco espacio libre (< %d MB): no se guarda el histórico", cfg.MinFreeMB)
				lowSpace = true
			}
			return // se descartan estos datos; se reanuda al liberar espacio
		}
		lowSpace = false
		for _, b := range pending {
			if err := appendLine(historyFile(cfg.DataDir), b); err != nil {
				logf("no se pudo guardar el histórico: %v", err)
			}
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	tick := time.NewTicker(time.Duration(cfg.IntervalSec) * time.Second)
	flushT := time.NewTicker(30 * time.Minute)
	compactT := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	defer flushT.Stop()
	defer compactT.Stop()

	warnedAcct := false
	sample := func() {
		now := time.Now()
		ifs, err := readNetDev(netDevPath)
		if err != nil {
			logf("lectura de interfaces: %v", err)
			return
		}
		leases, _ := parseLeases(leasesPath)
		hosts, _ := listHosts(leases)
		var ips []string
		names := map[string]string{}
		for _, h := range hosts {
			ips = append(ips, h.IP)
			names[h.IP] = h.Hostname
		}
		hs, err := collectAcct.poll(ips)
		if err != nil {
			if !warnedAcct {
				logf("sin datos por equipo: %v", err)
				warnedAcct = true
			}
			hs = nil
		}
		for _, a := range c.step(now, ifs, hs, names) {
			deliver(cfg, a)
		}
	}
	logf("colector iniciado (cada %ds, datos en %s)", cfg.IntervalSec, cfg.DataDir)
	sample()
	for {
		select {
		case <-tick.C:
			sample()
		case <-flushT.C:
			flush()
		case <-compactT.C:
			flush()
			compact()
		case s := <-sig:
			logf("señal %v: guardando y saliendo", s)
			flush()
			return nil
		}
	}
}

// deliver guarda el aviso, lo manda a syslog y, si hay URL, a un webhook.
func deliver(cfg Config, a Alert) {
	logf("AVISO %s: %s", a.Kind, a.Msg)
	if hasRoom(cfg.DataDir, 4<<10, uint64(cfg.MinFreeMB)<<20) {
		trimLines(alertsFile(cfg.DataDir), 256<<10, 200)
		appendLine(alertsFile(cfg.DataDir), a)
	}
	exec.Command("logger", "-t", "erx-tui", "-p", "daemon.warning", a.Msg).Run()
	if cfg.WebhookURL != "" {
		body, _ := json.Marshal(map[string]string{"text": "erx-tui: " + a.Msg, "kind": a.Kind, "key": a.Key, "time": a.Time})
		cl := http.Client{Timeout: 5 * time.Second}
		if resp, err := cl.Post(cfg.WebhookURL, "application/json", bytes.NewReader(body)); err != nil {
			logf("webhook: %v", err)
		} else {
			resp.Body.Close()
		}
	}
}

func loadAlerts(dir string, n int) []Alert {
	b, err := os.ReadFile(alertsFile(dir))
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var out []Alert
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- { // más recientes primero
		var a Alert
		if json.Unmarshal([]byte(lines[i]), &a) == nil && a.Msg != "" {
			out = append(out, a)
		}
	}
	return out
}

// ---------- estado y servicio de arranque ----------

func collectorRunning(dir string) (int, bool) {
	b, err := os.ReadFile(pidFile(dir))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	err = syscall.Kill(pid, 0)
	return pid, err == nil || errors.Is(err, syscall.EPERM)
}

const bootScript = "/config/scripts/post-config.d/erx-tui-collect.sh"

// installService arranca el colector ahora y lo registra para el arranque
// del router (EdgeOS ejecuta /config/scripts/post-config.d/* como root).
func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	script := "#!/bin/sh\n# Creado por erx-tui --install-service\n" +
		"nohup '" + exe + "' --collect >/dev/null 2>&1 &\n"
	if err := os.MkdirAll(filepath.Dir(bootScript), 0o755); err != nil {
		return fmt.Errorf("%w (¿falta sudo?)", err)
	}
	if err := os.WriteFile(bootScript, []byte(script), 0o755); err != nil {
		return fmt.Errorf("%w (¿falta sudo?)", err)
	}
	fmt.Println("Script de arranque:", bootScript)
	return startCollector(exe)
}

func startCollector(exe string) error {
	cfg, _ := loadConfig()
	if pid, ok := collectorRunning(cfg.DataDir); ok {
		fmt.Println("El colector ya está en marcha (pid", strconv.Itoa(pid)+")")
		return nil
	}
	cmd := exec.Command(exe, "--collect")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	cmd.Process.Release()
	fmt.Println("Colector iniciado. Datos en", cfg.DataDir)
	return nil
}

func removeService() error {
	cfg, _ := loadConfig()
	if err := os.Remove(bootScript); err != nil && !os.IsNotExist(err) {
		return err
	}
	if pid, ok := collectorRunning(cfg.DataDir); ok {
		syscall.Kill(pid, syscall.SIGTERM)
		fmt.Println("Colector detenido (pid", strconv.Itoa(pid)+")")
	}
	fmt.Println("Servicio retirado. El histórico se conserva en", cfg.DataDir)
	return nil
}
