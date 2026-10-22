package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

var dnsTypes = []string{"A", "AAAA", "MX", "TXT", "NS", "CNAME", "PTR"}

type DNSAttempt struct {
	Elapsed time.Duration
	Answers []string
	Err     error
}

// systemDNS devuelve el primer "nameserver" de /etc/resolv.conf o 127.0.0.1.
func systemDNS() string {
	if f, err := os.Open("/etc/resolv.conf"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) >= 2 && fs[0] == "nameserver" {
				return fs[1]
			}
		}
	}
	return "127.0.0.1"
}

// dnsTest lanza `n` consultas seguidas contra `server` (la primera suele
// ir sin caché, las siguientes muestran si el servidor cachea).
func dnsTest(name, server, qtype string, n int) []DNSAttempt {
	if _, _, err := net.SplitHostPort(server); err != nil {
		if strings.Contains(server, ":") { // IPv6 sin puerto
			server = "[" + server + "]"
		}
		server += ":53"
	}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "udp", server)
		},
	}
	out := make([]DNSAttempt, 0, n)
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t0 := time.Now()
		ans, err := lookup(ctx, r, name, qtype)
		cancel()
		out = append(out, DNSAttempt{Elapsed: time.Since(t0), Answers: ans, Err: err})
	}
	return out
}

func lookup(ctx context.Context, r *net.Resolver, name, qtype string) ([]string, error) {
	switch qtype {
	case "A", "AAAA":
		network := "ip4"
		if qtype == "AAAA" {
			network = "ip6"
		}
		ips, err := r.LookupIP(ctx, network, name)
		var s []string
		for _, ip := range ips {
			s = append(s, ip.String())
		}
		return s, err
	case "MX":
		mx, err := r.LookupMX(ctx, name)
		var s []string
		for _, m := range mx {
			s = append(s, fmt.Sprintf("%d %s", m.Pref, m.Host))
		}
		return s, err
	case "NS":
		ns, err := r.LookupNS(ctx, name)
		var s []string
		for _, n := range ns {
			s = append(s, n.Host)
		}
		return s, err
	case "TXT":
		return r.LookupTXT(ctx, name)
	case "CNAME":
		c, err := r.LookupCNAME(ctx, name)
		return []string{c}, err
	case "PTR":
		return r.LookupAddr(ctx, name)
	}
	return nil, fmt.Errorf("tipo %q no soportado", qtype)
}
