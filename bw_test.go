package main

import (
	"os"
	"testing"
	"time"
)

func TestNetDev(t *testing.T) {
	a, err := readNetDev("testdata/netdev")
	if err != nil || a["eth1"].TX != 4000000 || a["eth0.10"].RX != 5 {
		t.Fatalf("%v %+v", err, a)
	}
	s := newNetSampler()
	t0 := time.Now()
	s.update(t0, a)
	b := map[string]IfStat{}
	for k, v := range a {
		b[k] = v
	}
	b["eth0"] = IfStat{RX: 1000000 + 2_000_000, TX: 500000 + 100_000}
	b["eth0.10"] = IfStat{RX: 5 + 999, TX: 5} // VLAN: no entra en el total
	s.update(t0.Add(2*time.Second), b)
	r := s.rates["eth0"]
	if r.RX != 1_000_000 || r.TX != 50_000 || s.TotRX != 1_000_000 {
		t.Errorf("rates %+v tot %v", r, s.TotRX)
	}
	if _, ok := s.rates["lo"]; ok {
		t.Error("lo no debe aparecer")
	}
	if _, ok := s.rates["ifb0"]; ok {
		t.Error("ifb0 no debe aparecer")
	}
}

func TestParseAcct(t *testing.T) {
	data, _ := os.ReadFile("testdata/iptables")
	c := parseAcct(string(data))
	if c["192.168.1.50"] != (HostCounters{Up: 12345, Down: 987654}) || c["192.168.1.9"] != (HostCounters{Up: 0, Down: 2000}) || len(c) != 2 {
		t.Fatalf("%+v", c)
	}
	h := newHostSampler()
	t0 := time.Now()
	h.update(t0, c, map[string]string{"192.168.1.50": "iphone"})
	c2 := map[string]HostCounters{"192.168.1.50": {Up: 12345 + 100, Down: 987654 + 1000}, "192.168.1.9": {Down: 2000}}
	h.update(t0.Add(2*time.Second), c2, nil)
	r := h.sorted()
	if r[0].IP != "192.168.1.50" || r[0].Down != 500 || r[0].Up != 50 {
		t.Errorf("%+v", r[0])
	}
}

func TestFormat(t *testing.T) {
	if rate(125000) != "1.0 Mb/s" || rate(0) != "0 b/s" || size(1<<20) != "1.0 MB" {
		t.Errorf("%s %s %s", rate(125000), rate(0), size(1<<20))
	}
	if spark([]float64{0, 1, 2}) != "▁▄█" && spark([]float64{0, 1, 2}) != "▁▅█" {
		t.Errorf("spark %s", spark([]float64{0, 1, 2}))
	}
}

func TestOffload(t *testing.T) {
	os.WriteFile(t.TempDir()+"/x", nil, 0o600)
	d := t.TempDir()
	os.WriteFile(d+"/c", []byte("system {\n    offload {\n        hwnat enable\n    }\n}\n"), 0o600)
	if !offloadOn(d+"/c") || offloadOn("testdata/config.boot") {
		t.Error("offload")
	}
}

func TestSpeedLive(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	runSpeedTest()
	s := speed.snapshot()
	if s.Err != nil {
		t.Skipf("sin red: %v", s.Err)
	}
	t.Logf("lat %v jitter %v down %s up %s", s.Latency, s.Jitter, mbps(s.Down), mbps(s.Up))
	if s.Down <= 0 || s.Up <= 0 {
		t.Error("velocidades vacías")
	}
}
