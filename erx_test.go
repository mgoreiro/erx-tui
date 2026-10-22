package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLeases(t *testing.T) {
	ls, err := parseLeases("testdata/dhcpd.leases")
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 3 {
		t.Fatalf("want 3 leases, got %d: %+v", len(ls), ls)
	}
	// ordenadas por IP y la última entrada de .50 gana
	if ls[0].IP != "192.168.1.9" || !ls[0].Ends.IsZero() {
		t.Errorf("bad first lease %+v", ls[0])
	}
	if ls[1].Ends.Year() != 2099 || ls[1].Hostname != "iphone-miguel" || ls[1].MAC != "aa:bb:cc:00:11:22" {
		t.Errorf("bad .50 lease %+v", ls[1])
	}
	if ls[2].Active() {
		t.Errorf(".77 should be free")
	}
}

func TestARP(t *testing.T) {
	hs, err := parseProcARP("testdata/arp")
	if err != nil || len(hs) != 3 {
		t.Fatalf("%v %+v", err, hs)
	}
}

func TestIPNeigh(t *testing.T) {
	hs := parseIPNeigh("192.168.1.5 dev eth1 lladdr AA:BB:CC:DD:EE:FF REACHABLE\n192.168.1.6 dev eth1  FAILED\n10.0.0.1 dev eth0 lladdr 02:00:00:00:00:01 STALE\n")
	if len(hs) != 2 || hs[0].MAC != "aa:bb:cc:dd:ee:ff" || hs[1].State != "STALE" || hs[1].Iface != "eth0" {
		t.Fatalf("%+v", hs)
	}
}

func TestTasks(t *testing.T) {
	ts, err := listTasks("testdata/config.boot")
	if err != nil || len(ts) != 3 {
		t.Fatalf("%v %+v", err, ts)
	}
	if !ts[0].IsReboot() || ts[0].Describe() != "Cada lunes a las 04:00" {
		t.Errorf("%+v %q", ts[0], ts[0].Describe())
	}
	if ts[1].IsReboot() || ts[1].Cron != "cada 1d" {
		t.Errorf("%+v", ts[1])
	}
	if ts[2].Describe() != "Todos los días a las 03:30" {
		t.Errorf("%q", ts[2].Describe())
	}
}

func TestCronAndNames(t *testing.T) {
	if cronSpec(1, 4, 0) != "0 4 * * 1" || cronSpec(-1, 3, 30) != "30 3 * * *" {
		t.Error("cronSpec")
	}
	if taskName(0, 3, 5) != "tui-reboot-dom-0305" || !safeName.MatchString(taskName(-1, 3, 5)) {
		t.Error("taskName")
	}
	if deleteTask("x; rm -rf /") == nil {
		t.Error("nombre peligroso aceptado")
	}
	if _, err := addReboot(9, 1, 1); err == nil {
		t.Error("día inválido aceptado")
	}
}

// Renderiza cada pestaña para inspección visual: go test -run TestRender -v
func TestRender(t *testing.T) {
	leasesPath, configPath, arpPath = "testdata/dhcpd.leases", "testdata/config.boot", "testdata/arp"
	m := newModel()
	m.w, m.h = 90, 20
	m.data = loadData().(dataMsg)
	var tm tea.Model = m
	for _, k := range []string{"1", "2", "3", "4"} {
		if k == "1" {
			tm, _ = tm.(model).onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		} else {
			tm, _ = tm.(model).onKey(tea.KeyMsg{Type: tea.KeyTab})
		}
		if k == "3" {
			mm := tm.(model)
			mm.dnsRes = []DNSAttempt{{Answers: []string{"142.250.1.1"}}, {Err: fmt.Errorf("timeout")}}
			mm.dnsRan = "A google.com @127.0.0.1"
			tm = mm
		}
		if k == "4" {
			tm, _ = tm.(model).onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
		}
		fmt.Printf("=== tab %s ===\n%s\n", k, tm.View())
	}
}

func TestRenderBW(t *testing.T) {
	leasesPath, configPath, arpPath = "testdata/dhcpd.leases", "testdata/config.boot", "testdata/arp"
	m := newModel()
	m.w, m.h = 100, 20
	m.data = loadData().(dataMsg)
	a, _ := readNetDev("testdata/netdev")
	t0 := time.Now()
	m.ifs.update(t0, a)
	b := map[string]IfStat{"eth0": {3000000, 600000}, "eth1": {2100000, 9000000}, "lo": {}}
	m.ifs.update(t0.Add(time.Second), b)
	data, _ := os.ReadFile("testdata/iptables")
	m.hbw.update(t0, parseAcct(string(data)), map[string]string{"192.168.1.50": "iphone-miguel"})
	m.tab = tabIfaces
	fmt.Println(m.View())
	m.tab = tabHostBW
	fmt.Println(m.View())
	m.tab = tabSpeed
	speed.set(func(s *speedState) {
		s.speedData = speedData{Phase: "fin", Latency: 12 * time.Millisecond, Jitter: 2 * time.Millisecond, Down: 95e6, Up: 20e6}
	})
	fmt.Println(m.View())
}
