package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var netDevPath = envOr("ERX_NETDEV", "/proc/net/dev")

// ---------- consumo por interfaz (/proc/net/dev) ----------

type IfStat struct{ RX, TX uint64 }

func readNetDev(path string) (map[string]IfStat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]IfStat{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue // cabeceras
		}
		fs := strings.Fields(rest)
		if len(fs) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fs[0], 10, 64)
		tx, _ := strconv.ParseUint(fs[8], 10, 64)
		out[strings.TrimSpace(name)] = IfStat{rx, tx}
	}
	return out, sc.Err()
}

// interfaces que no aportan nada al usuario
func hiddenIface(n string) bool {
	if n == "lo" {
		return true
	}
	for _, p := range []string{"ifb", "gre", "erspan", "ip6", "sit", "tunl", "teql", "imq", "dummy"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// físicas: eth0, eth1… (sin subinterfaces VLAN como eth0.10)
func physicalIface(n string) bool {
	return strings.HasPrefix(n, "eth") && !strings.Contains(n, ".")
}

type IfRate struct {
	Name         string
	RX, TX       float64   // bytes/s
	TotRX, TotTX uint64    // bytes desde que arrancó la app
	Hist         []float64 // RX+TX, últimas muestras
	Link         string
}

type netSampler struct {
	prev         map[string]IfStat
	base         map[string]IfStat
	prevT        time.Time
	rates        map[string]*IfRate
	TotRX, TotTX float64 // suma de interfaces físicas, bytes/s
	SumRX, SumTX uint64
}

const histLen = 40

func newNetSampler() *netSampler {
	return &netSampler{rates: map[string]*IfRate{}}
}

func (s *netSampler) update(t time.Time, cur map[string]IfStat) {
	if s.prev == nil {
		s.base = cur
	} else if dt := t.Sub(s.prevT).Seconds(); dt > 0 {
		s.TotRX, s.TotTX, s.SumRX, s.SumTX = 0, 0, 0, 0
		for name, c := range cur {
			if hiddenIface(name) {
				continue
			}
			p, ok := s.prev[name]
			if !ok {
				continue
			}
			r := s.rates[name]
			if r == nil {
				r = &IfRate{Name: name}
				s.rates[name] = r
			}
			r.RX = float64(delta(c.RX, p.RX)) / dt
			r.TX = float64(delta(c.TX, p.TX)) / dt
			r.TotRX = delta(c.RX, s.base[name].RX)
			r.TotTX = delta(c.TX, s.base[name].TX)
			r.Hist = append(r.Hist, r.RX+r.TX)
			if len(r.Hist) > histLen {
				r.Hist = r.Hist[len(r.Hist)-histLen:]
			}
			r.Link = linkInfo(name)
			if physicalIface(name) {
				s.TotRX += r.RX
				s.TotTX += r.TX
				s.SumRX += r.TotRX
				s.SumTX += r.TotTX
			}
		}
	}
	s.prev, s.prevT = cur, t
}

func (s *netSampler) sorted() []*IfRate {
	var out []*IfRate
	for _, r := range s.rates {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// delta tolera reinicios de contador (devuelve 0).
func delta(cur, prev uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

func linkInfo(name string) string {
	state := readTrim("/sys/class/net/" + name + "/operstate")
	if state == "" {
		return "-"
	}
	if state != "up" {
		return state
	}
	if sp, err := strconv.Atoi(readTrim("/sys/class/net/" + name + "/speed")); err == nil && sp > 0 {
		return fmt.Sprintf("up %dM", sp)
	}
	return "up"
}

func readTrim(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ---------- consumo por equipo (contadores de iptables) ----------
//
// Se crea una cadena propia ("ERXTUI") enganchada al principio de FORWARD con
// dos reglas SIN objetivo por equipo (-s IP y -d IP). Una regla sin objetivo
// solo cuenta y deja seguir al paquete, así que no altera el filtrado.
// Todo vive en memoria: desaparece al reiniciar y la app la borra al salir.
//
// Limitación: el tráfico acelerado por hardware (offload/hwnat) no pasa
// por iptables y no se contabiliza.

type hostAcct struct {
	chain   string
	mu      sync.Mutex
	started bool
	tracked map[string]bool
}

func newAcct(chain string) *hostAcct {
	return &hostAcct{chain: chain, tracked: map[string]bool{}}
}

// acct sirve a la pestaña "Por equipo"; collectAcct al colector en segundo
// plano (cadenas distintas para que no se pisen).
var (
	acct        = newAcct("ERXTUI")
	collectAcct = newAcct("ERXTUID")
)

func ipt(args ...string) ([]byte, error) {
	return exec.Command("sudo", append([]string{"-n", "iptables"}, args...)...).CombinedOutput()
}

type HostCounters struct{ Up, Down uint64 } // Up: origen=host, Down: destino=host

// poll prepara la contabilidad (la primera vez), añade reglas para equipos
// nuevos y devuelve los contadores acumulados por IP.
func (a *hostAcct) poll(ips []string) (map[string]HostCounters, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.started {
		ipt("-N", a.chain) // ya existe: se ignora
		ipt("-F", a.chain)
		if _, err := ipt("-C", "FORWARD", "-j", a.chain); err != nil {
			if out, err := ipt("-I", "FORWARD", "1", "-j", a.chain); err != nil {
				return nil, fmt.Errorf("no se pudo crear la cadena (¿sudo/iptables?): %s", strings.TrimSpace(string(out)))
			}
		}
		a.started = true
		a.tracked = map[string]bool{}
	}
	for _, ip := range ips {
		if a.tracked[ip] || net.ParseIP(ip) == nil {
			continue
		}
		for _, dir := range []string{"-s", "-d"} {
			if out, err := ipt("-A", a.chain, dir, ip); err != nil {
				return nil, fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
		}
		a.tracked[ip] = true
	}
	out, err := ipt("-nvxL", a.chain)
	if err != nil {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return parseAcct(string(out)), nil
}

// parseAcct lee `iptables -nvxL`. Las reglas sin objetivo dejan la columna
// "target" vacía, así que se usan las dos últimas columnas (origen/destino).
func parseAcct(s string) map[string]HostCounters {
	res := map[string]HostCounters{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 {
			continue
		}
		bytes, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue // cabeceras
		}
		src, dst := f[len(f)-2], f[len(f)-1]
		switch {
		case dst == "0.0.0.0/0" && src != "0.0.0.0/0":
			c := res[src]
			c.Up = bytes
			res[src] = c
		case src == "0.0.0.0/0" && dst != "0.0.0.0/0":
			c := res[dst]
			c.Down = bytes
			res[dst] = c
		}
	}
	return res
}

func (a *hostAcct) cleanup() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started {
		return
	}
	ipt("-D", "FORWARD", "-j", a.chain)
	ipt("-F", a.chain)
	ipt("-X", a.chain)
	a.started = false
}

type HostRate struct {
	IP, Name       string
	Up, Down       float64 // bytes/s
	TotUp, TotDown uint64
	prev           HostCounters
}

type hostSampler struct {
	prevT time.Time
	rates map[string]*HostRate
}

func newHostSampler() *hostSampler { return &hostSampler{rates: map[string]*HostRate{}} }

func (s *hostSampler) update(t time.Time, cur map[string]HostCounters, names map[string]string) {
	dt := t.Sub(s.prevT).Seconds()
	for ip, c := range cur {
		r := s.rates[ip]
		first := r == nil
		if first {
			r = &HostRate{IP: ip}
			s.rates[ip] = r
		}
		r.Name = names[ip]
		if !first && dt > 0 {
			r.Up = float64(delta(c.Up, r.prev.Up)) / dt
			r.Down = float64(delta(c.Down, r.prev.Down)) / dt
		}
		r.TotUp, r.TotDown, r.prev = c.Up, c.Down, c
	}
	s.prevT = t
}

// sorted: primero el que más consume ahora, luego por total acumulado.
func (s *hostSampler) sorted() []*HostRate {
	var out []*HostRate
	for _, r := range s.rates {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := a.Up+a.Down, b.Up+b.Down; ra != rb {
			return ra > rb
		}
		if ta, tb := a.TotUp+a.TotDown, b.TotUp+b.TotDown; ta != tb {
			return ta > tb
		}
		return ipLess(a.IP, b.IP)
	})
	return out
}

// offloadOn avisa de que hwnat está activo (el tráfico acelerado no se cuenta).
func offloadOn(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	off := parseConfig(string(data)).child("system").child("offload")
	if off == nil {
		return false
	}
	for _, c := range off.children {
		if (c.name == "hwnat" || c.name == "ipv4") && strings.Contains(c.args, "enable") {
			return true
		}
		for _, cc := range c.children {
			if cc.name == "forwarding" && strings.Contains(cc.args, "enable") {
				return true
			}
		}
	}
	return false
}

// ---------- formato ----------

// rate: bytes/s -> "12.3 Mb/s"
func rate(bps float64) string {
	bits := bps * 8
	switch {
	case bits >= 1e9:
		return fmt.Sprintf("%.2f Gb/s", bits/1e9)
	case bits >= 1e6:
		return fmt.Sprintf("%.1f Mb/s", bits/1e6)
	case bits >= 1e3:
		return fmt.Sprintf("%.0f kb/s", bits/1e3)
	}
	return fmt.Sprintf("%.0f b/s", bits)
}

func size(b uint64) string {
	f := float64(b)
	switch {
	case f >= 1<<30:
		return fmt.Sprintf("%.2f GB", f/(1<<30))
	case f >= 1<<20:
		return fmt.Sprintf("%.1f MB", f/(1<<20))
	case f >= 1<<10:
		return fmt.Sprintf("%.0f KB", f/(1<<10))
	}
	return fmt.Sprintf("%d B", b)
}

func spark(h []float64) string {
	const bars = "▁▂▃▄▅▆▇█"
	var max float64
	for _, v := range h {
		if v > max {
			max = v
		}
	}
	r := []rune(bars)
	var b strings.Builder
	for _, v := range h {
		i := 0
		if max > 0 {
			i = int(v / max * float64(len(r)-1))
		}
		b.WriteRune(r[i])
	}
	return b.String()
}
