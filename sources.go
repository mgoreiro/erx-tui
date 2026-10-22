package main

import (
	"bufio"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Rutas de EdgeOS; se pueden sobrescribir con variables de entorno para
// probar en otra máquina.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	leasesPath = envOr("ERX_LEASES", "/var/run/dhcpd.leases")
	configPath = envOr("ERX_CONFIG", "/config/config.boot")
	arpPath    = envOr("ERX_ARP", "/proc/net/arp")
)

// ---------- Cesiones DHCP ----------

type Lease struct {
	IP       string
	MAC      string
	Hostname string
	Starts   time.Time
	Ends     time.Time // cero = "never"
	State    string
}

func (l Lease) Active() bool { return l.State == "active" }

// parseLeases lee el formato ISC dhcpd.leases. Si una IP aparece varias
// veces gana la última entrada (el fichero es append-only).
func parseLeases(path string) ([]Lease, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	byIP := map[string]*Lease{}
	var order []string
	var cur *Lease

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		line = strings.TrimSuffix(line, ";")
		switch {
		case strings.HasPrefix(line, "lease ") && strings.HasSuffix(line, "{"):
			ip := strings.Fields(line)[1]
			cur = &Lease{IP: ip}
		case line == "}" || line == "} ":
			if cur != nil {
				if _, ok := byIP[cur.IP]; !ok {
					order = append(order, cur.IP)
				}
				byIP[cur.IP] = cur
				cur = nil
			}
		case cur == nil:
		case strings.HasPrefix(line, "starts "):
			cur.Starts = parseLeaseTime(line[len("starts "):])
		case strings.HasPrefix(line, "ends "):
			cur.Ends = parseLeaseTime(line[len("ends "):])
		case strings.HasPrefix(line, "binding state "):
			cur.State = strings.TrimPrefix(line, "binding state ")
		case strings.HasPrefix(line, "hardware ethernet "):
			cur.MAC = strings.ToLower(strings.TrimPrefix(line, "hardware ethernet "))
		case strings.HasPrefix(line, "client-hostname "):
			cur.Hostname = strings.Trim(strings.TrimPrefix(line, "client-hostname "), `"`)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make([]Lease, 0, len(order))
	for _, ip := range order {
		out = append(out, *byIP[ip])
	}
	sort.Slice(out, func(i, j int) bool { return ipLess(out[i].IP, out[j].IP) })
	return out, nil
}

// parseLeaseTime acepta "3 2024/01/10 10:00:00" (UTC) o "never".
func parseLeaseTime(s string) time.Time {
	f := strings.Fields(s)
	if len(f) < 3 {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006/01/02 15:04:05", f[1]+" "+f[2], time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ---------- Equipos conectados ----------

type Host struct {
	IP       string
	MAC      string
	Iface    string
	State    string // REACHABLE, STALE, DELAY...
	Hostname string
}

// listHosts combina la tabla de vecinos del kernel con los nombres de
// las cesiones DHCP.
func listHosts(leases []Lease) ([]Host, error) {
	hosts, err := neighbors()
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, l := range leases {
		if l.Hostname != "" && l.Active() {
			names[l.IP] = l.Hostname
		}
	}
	for i := range hosts {
		hosts[i].Hostname = names[hosts[i].IP]
	}
	sort.Slice(hosts, func(i, j int) bool { return ipLess(hosts[i].IP, hosts[j].IP) })
	return hosts, nil
}

func neighbors() ([]Host, error) {
	if os.Getenv("ERX_ARP") == "" {
		if out, err := exec.Command("ip", "-4", "neigh", "show").Output(); err == nil {
			return parseIPNeigh(string(out)), nil
		}
	}
	return parseProcARP(arpPath)
}

// parseIPNeigh: "192.168.1.5 dev eth1 lladdr aa:bb:.. REACHABLE"
func parseIPNeigh(s string) []Host {
	var out []Host
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		h := Host{IP: f[0], State: f[len(f)-1]}
		for i := 1; i < len(f)-1; i++ {
			switch f[i] {
			case "dev":
				h.Iface = f[i+1]
			case "lladdr":
				h.MAC = strings.ToLower(f[i+1])
			}
		}
		if h.MAC == "" || h.State == "FAILED" || h.State == "INCOMPLETE" {
			continue
		}
		out = append(out, h)
	}
	return out
}

// parseProcARP: respaldo si no existe `ip`.
func parseProcARP(path string) ([]Host, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Host
	sc := bufio.NewScanner(f)
	sc.Scan() // cabecera
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 || f[2] == "0x0" {
			continue
		}
		out = append(out, Host{IP: f[0], MAC: strings.ToLower(f[3]), Iface: f[5], State: "-"})
	}
	return out, sc.Err()
}

func ipLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	if len(pa) != 4 || len(pb) != 4 {
		return a < b
	}
	for i := 0; i < 4; i++ {
		x, y := atoi(pa[i]), atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
