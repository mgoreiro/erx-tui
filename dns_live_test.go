package main

import "testing"

func TestDNSLive(t *testing.T) {
	r := dnsTest("example.com", "1.1.1.1", "A", 2)
	for _, a := range r {
		if a.Err != nil || len(a.Answers) == 0 {
			t.Skipf("sin red o DNS bloqueado: %v", a.Err)
		}
		t.Logf("%v %v", a.Elapsed, a.Answers)
	}
}
