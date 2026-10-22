package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

var dayShort = []string{"Dom", "Lun", "Mar", "Mié", "Jue", "Vie", "Sáb"}

func (m model) tabBar() string {
	var full strings.Builder
	width := 0
	for i, t := range tabTitles {
		if i == m.tab {
			full.WriteString(cTabOn.Render(t))
		} else {
			full.WriteString(cTabOff.Render(t))
		}
		width += len([]rune(t)) + 2
	}
	if width+len("ER-X TUI v"+version)+3 <= m.w {
		return full.String()
	}
	// estrecho: solo el título de la pestaña activa, el resto por número
	var b strings.Builder
	for i, t := range tabTitles {
		if i == m.tab {
			b.WriteString(cTabOn.Render(t))
		} else {
			b.WriteString(cTabOff.Render(t[:1]))
		}
	}
	return b.String()
}

func (m model) recentAlerts() int {
	n := 0
	for _, a := range m.data.alerts {
		if time.Since(a.when()) < 24*time.Hour {
			n++
		}
	}
	return n
}

// histSubject: el elegido, o eth0 / el primero si no hay elección válida.
func (m model) histSubject(ss []string) string {
	for _, s := range ss {
		if s == m.histSubj {
			return s
		}
	}
	for _, s := range ss {
		if s == "if:eth0" {
			return s
		}
	}
	if len(ss) > 0 {
		return ss[0]
	}
	return ""
}

type histRow struct {
	label string
	a, b  uint64
	extra string
}

func (m model) histRows() []histRow {
	ss := subjects(m.hist)
	if len(ss) == 0 {
		return nil
	}
	now := time.Now()
	var rows []histRow
	if m.histTop {
		since := now.Add(-24 * time.Hour)
		if m.histDaily {
			since = now.Add(-30 * 24 * time.Hour)
		}
		for _, h := range topHosts(m.hist, since) {
			rows = append(rows, histRow{label: h.IP, extra: h.Name, a: h.Down, b: h.Up})
		}
		return rows
	}
	n := 24
	if m.histDaily {
		n = 30
	}
	for _, p := range series(m.hist, m.histSubject(ss), m.histDaily, n, now) {
		l := p.Start.Format("02/01 15:00")
		if m.histDaily {
			l = dayShort[p.Start.Weekday()] + " " + p.Start.Format("02/01")
		}
		rows = append(rows, histRow{label: l, a: p.A, b: p.B})
	}
	return rows
}

func bar(v, max uint64, w int) string {
	if max == 0 {
		return ""
	}
	n := int(float64(v) / float64(max) * float64(w))
	if v > 0 && n == 0 {
		n = 1
	}
	return strings.Repeat("█", n)
}

func (m model) viewHist() string {
	if m.histErr != nil {
		if os.IsNotExist(m.histErr) {
			return cWarn.Render("Todavía no hay histórico.") + "\n" +
				"El colector guarda el tráfico en segundo plano. Actívalo en el router con:\n\n" +
				"  sudo /config/user-data/erx-tui --install-service\n\n" +
				cDim.Render("(la primera hora de datos se escribe a los 30 minutos)")
		}
		return cErr.Render("No se pudo leer el histórico: " + m.histErr.Error())
	}
	ss := subjects(m.hist)
	if len(ss) == 0 {
		return cDim.Render("Cargando histórico…")
	}
	rows := m.histRows()
	mode, view := "últimas 24 h", "serie"
	if m.histDaily {
		mode = "últimos 30 días"
	}
	title := subjectLabel(m.histSubject(ss), m.hist)
	if m.histTop {
		view, title = "top equipos", "Equipos por consumo"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s · %s   %s\n%s\n\n", cHead.Render(title), mode, view, m.collectorBadge(), m.storageLine())

	var max uint64
	for _, r := range rows {
		max = maxU(max, r.a+r.b)
	}
	head := fmt.Sprintf("%-12s %-12s %-12s %s", "PERIODO", "↓ BAJADA", "↑ SUBIDA", "")
	if m.histTop {
		head = fmt.Sprintf("%-16s %-18s %-12s %-12s", "IP", "NOMBRE", "↓ BAJADA", "↑ SUBIDA")
	}
	b.WriteString(cHead.Render(head) + "\n")
	from, to := m.listWindow(len(rows))
	for i := from; i < to; i++ {
		r := rows[i]
		var line string
		if m.histTop {
			name := r.extra
			if name == "" {
				name = "-"
			}
			line = fmt.Sprintf("%-16s %-18s %-12s %-12s %s", r.label, clip(name, 18), size(r.a), size(r.b), bar(r.a+r.b, max, 14))
		} else {
			line = fmt.Sprintf("%-12s %-12s %-12s %s", r.label, size(r.a), size(r.b), bar(r.a+r.b, max, 24))
		}
		b.WriteString(row(line, i == m.cursor[tabHist], m.w) + "\n")
	}
	return b.String()
}

func maxU(a, b uint64) uint64 {
	if b > a {
		return b
	}
	return a
}

// storageLine: espacio que usa el histórico y libre en la flash.
func (m model) storageLine() string {
	d := m.data
	s := "Histórico: " + size(uint64(d.histBytes)) + " (tope " + fmt.Sprint(d.cfg.HistoryMaxMB) + " MB)"
	if !d.freeOK {
		return cDim.Render(s)
	}
	s += " · libre en el router: " + size(d.freeBytes)
	if d.freeBytes < uint64(d.cfg.MinFreeMB)<<20 {
		return cErr.Render(s + " — por debajo de " + fmt.Sprint(d.cfg.MinFreeMB) + " MB: no se guarda más")
	}
	return cDim.Render(s)
}

func (m model) collectorBadge() string {
	if m.data.collOK {
		return cOK.Render(fmt.Sprintf("● colector activo (pid %d)", m.data.collPID))
	}
	return cErr.Render("○ colector parado")
}

func (m model) viewAlerts() string {
	var b strings.Builder
	cfg := m.data.cfg
	fmt.Fprintf(&b, "%s\n%s\n", m.collectorBadge(), m.storageLine())
	if m.data.cfgErr != nil {
		b.WriteString(cErr.Render("Configuración inválida ("+confPath+"): "+clip(m.data.cfgErr.Error(), m.w-30)) + "\n")
	}
	a := cfg.Alerts
	if !a.enabled() {
		b.WriteString(cWarn.Render("Avisos desactivados.") + cDim.Render(" Define umbrales en "+confPath+" (ver README).") + "\n")
	} else {
		var parts []string
		for n, v := range a.IfaceMbps {
			parts = append(parts, fmt.Sprintf("%s>%.0f Mb/s", n, v))
		}
		if a.HostMbps > 0 {
			parts = append(parts, fmt.Sprintf("equipo>%.0f Mb/s", a.HostMbps))
		}
		s := strings.Join(parts, ", ")
		if a.HostGBDay > 0 {
			s += fmt.Sprintf("  ·  cuota equipo %.0f GB/día", a.HostGBDay)
		}
		fmt.Fprintf(&b, "%s %s (sostenido %d min)\n", cDim.Render("Umbrales:"), s, a.SustainedMinutes)
	}
	if cfg.WebhookURL != "" {
		b.WriteString(cDim.Render("Webhook configurado.") + "\n")
	}
	b.WriteString("\n")
	al := m.data.alerts
	if len(al) == 0 {
		return b.String() + cDim.Render("Sin avisos registrados.")
	}
	b.WriteString(cHead.Render(fmt.Sprintf("%-12s %s", "CUÁNDO", "AVISO")) + "\n")
	from, to := m.listWindow(len(al))
	for i := from; i < to; i++ {
		line := fmt.Sprintf("%-12s %s", al[i].when().Local().Format("02/01 15:04"), al[i].Msg)
		b.WriteString(row(line, i == m.cursor[tabAlerts], m.w) + "\n")
	}
	return b.String()
}
