package main

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

// Histórico en formato JSON Lines, solo-añadir: cada línea son los
// incrementos de tráfico de una hora. Una misma hora puede aparecer en
// varias líneas (el colector escribe cada 30 min para no perder datos si
// se va la luz); al leer se suman. Así se escribe poco a la flash del
// router. Una vez al día se compacta y se descartan los datos viejos.

type Bucket struct {
	T  int64                `json:"t"`            // inicio de la hora (unix)
	If map[string][2]uint64 `json:"if,omitempty"` // iface -> [rx, tx]
	H  map[string][2]uint64 `json:"h,omitempty"`  // IP -> [bajada, subida] del equipo
	N  map[string]string    `json:"n,omitempty"`  // IP -> nombre
}

func hourStart(t time.Time) int64 { return t.Truncate(time.Hour).Unix() }

func newBucket(t int64) *Bucket {
	return &Bucket{T: t, If: map[string][2]uint64{}, H: map[string][2]uint64{}, N: map[string]string{}}
}

func (b *Bucket) addIf(name string, rx, tx uint64) {
	v := b.If[name]
	b.If[name] = [2]uint64{v[0] + rx, v[1] + tx}
}

func (b *Bucket) addHost(ip string, down, up uint64) {
	v := b.H[ip]
	b.H[ip] = [2]uint64{v[0] + down, v[1] + up}
}

func (b *Bucket) merge(o *Bucket) {
	for k, v := range o.If {
		b.addIf(k, v[0], v[1])
	}
	for k, v := range o.H {
		b.addHost(k, v[0], v[1])
	}
	for k, v := range o.N {
		if v != "" {
			b.N[k] = v
		}
	}
}

func (b *Bucket) empty() bool { return len(b.If) == 0 && len(b.H) == 0 }

func appendLine(path string, v any) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// loadHistory lee y consolida el fichero, ordenado por hora. Las líneas
// corruptas (p. ej. corte de luz a mitad de escritura) se ignoran.
func loadHistory(dir string) ([]Bucket, error) {
	f, err := os.Open(historyFile(dir))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	byT := map[int64]*Bucket{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var b Bucket
		if json.Unmarshal(sc.Bytes(), &b) != nil || b.T == 0 {
			continue
		}
		cur := byT[b.T]
		if cur == nil {
			cur = newBucket(b.T)
			byT[b.T] = cur
		}
		cur.merge(&b)
	}
	out := make([]Bucket, 0, len(byT))
	for _, b := range byT {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out, sc.Err()
}

// compactHistory reescribe el fichero consolidado y sin datos caducados.
func compactHistory(dir string, keep time.Duration, now time.Time) error {
	bs, err := loadHistory(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := now.Add(-keep).Unix()
	tmp := historyFile(dir) + ".tmp"
	os.Remove(tmp)
	for _, b := range bs {
		if b.T < cutoff {
			continue
		}
		if err := appendLine(tmp, b); err != nil {
			return err
		}
	}
	if _, err := os.Stat(tmp); err != nil { // todo caducado
		return os.Remove(historyFile(dir))
	}
	return os.Rename(tmp, historyFile(dir))
}

// ---------- consultas ----------

const subjectSum = "Σ eth*"

// subjects devuelve lo que se puede consultar: la suma de interfaces
// físicas, cada interfaz y cada equipo.
func subjects(bs []Bucket) []string {
	ifs, hs := map[string]bool{}, map[string]bool{}
	for _, b := range bs {
		for k := range b.If {
			ifs[k] = true
		}
		for k := range b.H {
			hs[k] = true
		}
	}
	out := []string{}
	for k := range ifs {
		out = append(out, "if:"+k)
	}
	sort.Strings(out)
	var hl []string
	for k := range hs {
		hl = append(hl, k)
	}
	sort.Slice(hl, func(i, j int) bool { return ipLess(hl[i], hl[j]) })
	for _, k := range hl {
		out = append(out, "host:"+k)
	}
	if len(ifs) > 0 {
		out = append([]string{subjectSum}, out...)
	}
	return out
}

func subjectLabel(s string, bs []Bucket) string {
	switch {
	case s == subjectSum:
		return "Suma de eth0..ethN"
	case strings.HasPrefix(s, "if:"):
		return "Interfaz " + strings.TrimPrefix(s, "if:")
	case strings.HasPrefix(s, "host:"):
		ip := strings.TrimPrefix(s, "host:")
		for i := len(bs) - 1; i >= 0; i-- {
			if n := bs[i].N[ip]; n != "" {
				return "Equipo " + ip + " (" + n + ")"
			}
		}
		return "Equipo " + ip
	}
	return s
}

func (b Bucket) value(subject string) (a, c uint64) {
	switch {
	case subject == subjectSum:
		for k, v := range b.If {
			if physicalIface(k) {
				a, c = a+v[0], c+v[1]
			}
		}
	case strings.HasPrefix(subject, "if:"):
		v := b.If[strings.TrimPrefix(subject, "if:")]
		a, c = v[0], v[1]
	case strings.HasPrefix(subject, "host:"):
		v := b.H[strings.TrimPrefix(subject, "host:")]
		a, c = v[0], v[1]
	}
	return
}

type Point struct {
	Start time.Time
	A, B  uint64 // bajada/subida (o rx/tx)
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.In(time.Local).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// series devuelve n periodos (horas o días) acabando en el actual,
// del más reciente al más antiguo.
func series(bs []Bucket, subject string, daily bool, n int, now time.Time) []Point {
	var pts []Point
	cur := now.In(time.Local).Truncate(time.Hour)
	step := func(t time.Time) time.Time { return t.Add(-time.Hour) }
	if daily {
		cur = startOfDay(now)
		step = func(t time.Time) time.Time { return startOfDay(t.Add(-time.Hour)) }
	}
	idx := map[int64]int{}
	for i := 0; i < n; i++ {
		pts = append(pts, Point{Start: cur})
		idx[cur.Unix()] = i
		cur = step(cur)
	}
	for _, b := range bs {
		t := time.Unix(b.T, 0)
		key := t.Truncate(time.Hour).Unix()
		if daily {
			key = startOfDay(t).Unix()
		}
		if i, ok := idx[key]; ok {
			a, c := b.value(subject)
			pts[i].A += a
			pts[i].B += c
		}
	}
	return pts
}

type HostTotal struct {
	IP, Name string
	Down, Up uint64
}

func topHosts(bs []Bucket, since time.Time) []HostTotal {
	m := map[string]*HostTotal{}
	for _, b := range bs {
		if b.T < since.Unix() {
			continue
		}
		for ip, v := range b.H {
			h := m[ip]
			if h == nil {
				h = &HostTotal{IP: ip}
				m[ip] = h
			}
			h.Down += v[0]
			h.Up += v[1]
			if n := b.N[ip]; n != "" {
				h.Name = n
			}
		}
	}
	var out []HostTotal
	for _, h := range m {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if ti, tj := out[i].Down+out[i].Up, out[j].Down+out[j].Up; ti != tj {
			return ti > tj
		}
		return ipLess(out[i].IP, out[j].IP)
	})
	return out
}
