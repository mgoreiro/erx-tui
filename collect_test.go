package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func testCfg() Config {
	c := defaultConfig()
	c.IntervalSec = 60
	c.Alerts = AlertsCfg{SustainedMinutes: 3, IfaceMbps: map[string]float64{"eth0": 80}, HostMbps: 80, HostGBDay: 1}
	return c
}

// 1 MB/s = 8 Mb/s -> 60 s * 12.5 MB/s = 100 Mb/s
const mb100 = 750_000_000

func TestSustainedAlert(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.Local)
	c := newCollector(testCfg(), nil, now)
	ifs := map[string]IfStat{"eth0": {}}
	var all []Alert
	for i := 0; i < 6; i++ {
		ifs = map[string]IfStat{"eth0": {RX: uint64(i) * mb100, TX: 0}}
		all = append(all, c.step(now.Add(time.Duration(i)*time.Minute), ifs, nil, nil)...)
	}
	// 5 intervalos >= umbral; primera muestra es base; avisa al 3er intervalo, una sola vez
	if len(all) != 1 || all[0].Kind != "sostenido" || all[0].Key != "if:eth0" {
		t.Fatalf("avisos: %+v", all)
	}
	// baja y vuelve a subir dentro de los 30 min -> el antirrebote lo suprime
	c.step(now.Add(6*time.Minute), map[string]IfStat{"eth0": {RX: 5 * mb100}}, nil, nil)
	got := 0
	for i := 7; i < 12; i++ {
		cur := map[string]IfStat{"eth0": {RX: 5*mb100 + uint64(i-6)*mb100}}
		got += len(c.step(now.Add(time.Duration(i)*time.Minute), cur, nil, nil))
	}
	if got != 0 {
		t.Errorf("antirrebote: %d avisos", got)
	}
	// pasada la franja de 30 min vuelve a avisar
	base := uint64(10 * mb100)
	c.step(now.Add(40*time.Minute), map[string]IfStat{"eth0": {RX: base}}, nil, nil) // baja (rearma)
	for i := 41; i < 46; i++ {
		cur := map[string]IfStat{"eth0": {RX: base + uint64(i-40)*mb100}}
		got += len(c.step(now.Add(time.Duration(i)*time.Minute), cur, nil, nil))
	}
	if got != 1 {
		t.Errorf("tras la franja debe avisar de nuevo: %d", got)
	}
}

func TestQuotaAndBuckets(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 30, 0, 0, time.Local)
	cfg := testCfg()
	cfg.Alerts.HostMbps = 0
	c := newCollector(cfg, nil, now)
	hs := map[string]HostCounters{"192.168.1.5": {}}
	c.step(now, nil, hs, nil)
	hs = map[string]HostCounters{"192.168.1.5": {Down: 600 << 20, Up: 100 << 20}}
	if a := c.step(now.Add(time.Minute), nil, hs, map[string]string{"192.168.1.5": "tv"}); len(a) != 0 {
		t.Fatalf("700 MB no supera 1 GB: %+v", a)
	}
	hs = map[string]HostCounters{"192.168.1.5": {Down: 1200 << 20, Up: 100 << 20}}
	a := c.step(now.Add(2*time.Minute), nil, hs, map[string]string{"192.168.1.5": "tv"})
	if len(a) != 1 || a[0].Kind != "cuota" {
		t.Fatalf("cuota: %+v", a)
	}
	hs = map[string]HostCounters{"192.168.1.5": {Down: 5000 << 20, Up: 100 << 20}}
	if a := c.step(now.Add(3*time.Minute), nil, hs, nil); len(a) != 0 {
		t.Errorf("la cuota solo avisa una vez al día: %+v", a)
	}
	bs := c.drain()
	if len(bs) != 1 || bs[0].H["192.168.1.5"][0] != 5000<<20 || bs[0].N["192.168.1.5"] != "tv" {
		t.Fatalf("bucket: %+v", bs)
	}
	if len(c.drain()) != 0 {
		t.Error("drain debe vaciar")
	}
}

func TestQuotaCountsHistoryOfToday(t *testing.T) {
	now := time.Date(2026, 10, 22, 18, 0, 0, 0, time.Local)
	hist := []Bucket{{T: now.Add(-3 * time.Hour).Unix(), H: map[string][2]uint64{"10.0.0.2": {900 << 20, 0}}}}
	c := newCollector(testCfg(), hist, now)
	c.step(now, nil, map[string]HostCounters{"10.0.0.2": {}}, nil)
	a := c.step(now.Add(time.Minute), nil, map[string]HostCounters{"10.0.0.2": {Down: 200 << 20}}, nil)
	if len(a) != 1 {
		t.Fatalf("debe sumar lo guardado hoy: %+v", a)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 22, 12, 10, 0, 0, time.Local)
	h := hourStart(now)
	mk := func(t0 int64, rx uint64) *Bucket {
		b := newBucket(t0)
		b.addIf("eth0", rx, rx/2)
		b.addHost("192.168.1.5", rx, 1)
		b.N["192.168.1.5"] = "tv"
		return b
	}
	for _, b := range []*Bucket{mk(h, 100), mk(h, 50), mk(h-3600, 10), mk(now.Add(-100*24*time.Hour).Unix()/3600*3600, 7)} {
		if err := appendLine(historyFile(dir), b); err != nil {
			t.Fatal(err)
		}
	}
	// línea corrupta tolerada
	os.OpenFile(historyFile(dir), os.O_APPEND|os.O_WRONLY, 0o644)
	f, _ := os.OpenFile(historyFile(dir), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{\"t\":12\n")
	f.Close()

	bs, err := loadHistory(dir)
	if err != nil || len(bs) != 3 {
		t.Fatalf("%v %d", err, len(bs))
	}
	if err := compactHistory(dir, 90*24*time.Hour, 0, 0, now); err != nil {
		t.Fatal(err)
	}
	bs, _ = loadHistory(dir)
	if len(bs) != 2 {
		t.Fatalf("tras compactar quedan %d", len(bs))
	}

	pts := series(bs, "if:eth0", false, 3, now)
	if len(pts) != 3 || pts[0].A != 150 || pts[0].B != 75 || pts[1].A != 10 || pts[2].A != 0 {
		t.Errorf("series horas: %+v", pts)
	}
	days := series(bs, subjectSum, true, 2, now)
	if days[0].A != 160 { // hoy: 150 + 10 (si no cruza medianoche)
		t.Errorf("series días: %+v", days)
	}
	top := topHosts(bs, now.Add(-24*time.Hour))
	if len(top) != 1 || top[0].Name != "tv" || top[0].Down != 160 {
		t.Errorf("top: %+v", top)
	}
	ss := subjects(bs)
	if ss[0] != subjectSum || ss[1] != "if:eth0" || ss[2] != "host:192.168.1.5" {
		t.Errorf("subjects: %v", ss)
	}
}

func TestConfig(t *testing.T) {
	d := t.TempDir()
	confPath = d + "/c.json"
	if c, err := loadConfig(); err != nil || c.Alerts.enabled() {
		t.Fatal("sin fichero: valores por defecto y avisos desactivados")
	}
	os.WriteFile(confPath, []byte(`{"interval_seconds":30,"alerts":{"host_gb_day":50,"iface_mbps":{"eth0":400}}}`), 0o644)
	c, err := loadConfig()
	if err != nil || c.IntervalSec != 30 || !c.Alerts.enabled() || c.Alerts.SustainedMinutes != 5 || c.RetentionDays != 90 {
		t.Fatalf("%v %+v", err, c)
	}
	os.WriteFile(confPath, []byte(`{no json`), 0o644)
	if _, err := loadConfig(); err == nil {
		t.Error("JSON inválido debe dar error")
	}
}

func TestCompactRespectsMaxSize(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	for i := 0; i < 200; i++ {
		b := newBucket(hourStart(now.Add(-time.Duration(i) * time.Hour)))
		b.addIf("eth0", 1000, 1000)
		b.addHost("192.168.1.5", 1000, 1000)
		appendLine(historyFile(dir), b)
	}
	before := fileSize(historyFile(dir))
	if err := compactHistory(dir, 90*24*time.Hour, before/2, 0, now); err != nil {
		t.Fatal(err)
	}
	after := fileSize(historyFile(dir))
	if after > before/2 || after == 0 {
		t.Fatalf("tamaño %d, tope %d", after, before/2)
	}
	bs, _ := loadHistory(dir)
	if bs[len(bs)-1].T != hourStart(now) { // se conserva lo más reciente
		t.Error("debe conservar las horas recientes y descartar las antiguas")
	}
	// sin espacio suficiente no toca nada
	if err := compactHistory(dir, 90*24*time.Hour, 0, 1<<62, now); err == nil {
		t.Error("debe negarse a compactar sin espacio")
	}
	if fileSize(historyFile(dir)) != after {
		t.Error("el fichero no debe cambiar")
	}
}

func TestTrimLinesAndFree(t *testing.T) {
	p := t.TempDir() + "/a.jsonl"
	for i := 0; i < 500; i++ {
		appendLine(p, Alert{Msg: "x"})
	}
	if err := trimLines(p, 1000, 10); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if n := len(loadAlertsFrom(string(b))); n != 10 {
		t.Errorf("quedan %d líneas", n)
	}
	if f, err := freeBytes(t.TempDir() + "/no/existe"); err != nil || f == 0 {
		t.Errorf("freeBytes: %d %v", f, err)
	}
}

func loadAlertsFrom(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestNoZerosAndAlertGap(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.Local)
	cfg := testCfg()
	cfg.Alerts.HostGBDay = 0
	c := newCollector(cfg, nil, now)
	c.step(now, map[string]IfStat{"eth0": {}, "eth1": {}}, map[string]HostCounters{"10.0.0.2": {}}, nil)
	c.step(now.Add(time.Minute), map[string]IfStat{"eth0": {RX: 5}, "eth1": {}}, map[string]HostCounters{"10.0.0.2": {}}, nil)
	b := c.drain()[0]
	if _, ok := b.If["eth1"]; ok || len(b.H) != 0 || len(b.N) != 0 {
		t.Errorf("no deben guardarse ceros: %+v", b)
	}
	// oscilación alrededor del umbral: un aviso por franja de 30 min
	var n int
	rx := uint64(0)
	prev := map[string]IfStat{"eth0": {}}
	c = newCollector(cfg, nil, now)
	c.step(now, prev, nil, nil)
	for i := 1; i <= 40; i++ {
		if i%5 != 0 { // 4 minutos alto, 1 bajo -> rearma cada 5 min
			rx += mb100
		}
		cur := map[string]IfStat{"eth0": {RX: rx}}
		n += len(c.step(now.Add(time.Duration(i)*time.Minute), cur, nil, nil))
	}
	if n != 2 { // 40 min: avisos en ~t=3 y ~t=33
		t.Errorf("avisos: %d", n)
	}
}
