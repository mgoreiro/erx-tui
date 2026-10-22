package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Test de velocidad contra los endpoints públicos de Cloudflare
// (speed.cloudflare.com). Se mide desde el propio router, así que el
// resultado es el de la conexión WAN, no el de tu portátil.

const (
	speedHost    = "https://speed.cloudflare.com"
	speedStreams = 4
	speedPhase   = 8 * time.Second
	speedWarmup  = 1500 * time.Millisecond
)

type speedData struct {
	Phase    string // "", latencia, descarga, subida, fin
	Latency  time.Duration
	Jitter   time.Duration
	Down, Up float64 // bits/s finales
	Live     float64 // bits/s instantáneos de la fase en curso
	Err      error
}

type speedState struct {
	mu sync.Mutex
	speedData
}

var speed = &speedState{}

func (s *speedState) set(f func(*speedState)) {
	s.mu.Lock()
	f(s)
	s.mu.Unlock()
}

func (s *speedState) snapshot() speedData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.speedData
}

type speedDoneMsg struct{}

func newSpeedClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		// HTTP/1.1: queremos varias conexiones TCP en paralelo, no una HTTP/2.
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConnsPerHost: speedStreams * 2,
		DisableCompression:  true,
		TLSHandshakeTimeout: 8 * time.Second,
	}}
}

func runSpeedTest() tea.Msg {
	speed.set(func(s *speedState) { s.speedData = speedData{Phase: "latencia"} })
	c := newSpeedClient()
	defer c.CloseIdleConnections()

	lat, jit, err := measureLatency(c)
	if err != nil {
		speed.set(func(s *speedState) { s.Err, s.Phase = err, "fin" })
		return speedDoneMsg{}
	}
	speed.set(func(s *speedState) { s.Latency, s.Jitter, s.Phase, s.Live = lat, jit, "descarga", 0 })

	down, err := measureThroughput(c, false)
	if err != nil {
		speed.set(func(s *speedState) { s.Err, s.Phase = err, "fin" })
		return speedDoneMsg{}
	}
	speed.set(func(s *speedState) { s.Down, s.Phase, s.Live = down, "subida", 0 })

	up, err := measureThroughput(c, true)
	speed.set(func(s *speedState) { s.Up, s.Err, s.Phase, s.Live = up, err, "fin", 0 })
	return speedDoneMsg{}
}

// measureLatency: tiempo hasta el primer byte con la conexión ya abierta.
func measureLatency(c *http.Client) (lat, jitter time.Duration, err error) {
	var ds []time.Duration
	for i := 0; i < 8; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", speedHost+"/__down?bytes=0", nil)
		t0 := time.Now()
		resp, e := c.Do(req)
		if e != nil {
			cancel()
			return 0, 0, fmt.Errorf("sin conexión con %s: %w", speedHost, e)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		cancel()
		if i > 0 { // la primera incluye TCP+TLS
			ds = append(ds, time.Since(t0))
		}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	var sum time.Duration
	for i := 1; i < len(ds); i++ {
		sum += absDur(ds[i] - ds[i-1])
	}
	return ds[len(ds)/2], sum / time.Duration(len(ds)-1), nil
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// measureThroughput abre speedStreams conexiones durante speedPhase y
// devuelve bits/s medidos tras el calentamiento (slow start).
func measureThroughput(c *http.Client, upload bool) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), speedPhase)
	defer cancel()

	var total atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	for i := 0; i < speedStreams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				var err error
				if upload {
					err = oneUpload(ctx, c, &total)
				} else {
					err = oneDownload(ctx, c, &total)
				}
				if err != nil && ctx.Err() == nil {
					firstErr.CompareAndSwap(nil, err)
					return
				}
			}
		}()
	}

	t0 := time.Now()
	var markBytes int64
	var markT time.Time
	prev, prevT := int64(0), t0
	tk := time.NewTicker(250 * time.Millisecond)
	defer tk.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case now := <-tk.C:
			cur := total.Load()
			live := float64(cur-prev) * 8 / now.Sub(prevT).Seconds()
			speed.set(func(s *speedState) { s.Live = live })
			prev, prevT = cur, now
			if markT.IsZero() && now.Sub(t0) >= speedWarmup {
				markBytes, markT = cur, now
			}
		}
	}
	wg.Wait()
	end := time.Now()
	if markT.IsZero() {
		if e, ok := firstErr.Load().(error); ok {
			return 0, e
		}
		return 0, fmt.Errorf("la prueba terminó demasiado pronto")
	}
	return float64(total.Load()-markBytes) * 8 / end.Sub(markT).Seconds(), nil
}

func oneDownload(ctx context.Context, c *http.Client, total *atomic.Int64) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", speedHost+"/__down?bytes=50000000", nil)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 64<<10)
	for {
		n, err := resp.Body.Read(buf)
		total.Add(int64(n))
		if err != nil {
			return nil
		}
	}
}

type zeroReader struct {
	left  int64
	total *atomic.Int64
}

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > z.left {
		n = z.left
	}
	clear(p[:n])
	z.left -= n
	z.total.Add(n)
	return int(n), nil
}

func oneUpload(ctx context.Context, c *http.Client, total *atomic.Int64) error {
	const size = 8 << 20
	req, _ := http.NewRequestWithContext(ctx, "POST", speedHost+"/__up", &zeroReader{left: size, total: total})
	req.ContentLength = size
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func mbps(bits float64) string { return fmt.Sprintf("%.1f Mb/s", bits/1e6) }
