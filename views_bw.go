package main

import (
	"fmt"
	"strings"
	"time"
)

func (m model) viewSpeed() string {
	s := speed.snapshot()
	var b strings.Builder
	b.WriteString(cHead.Render("Test de velocidad de la conexión a Internet") + "\n")
	b.WriteString(cDim.Render("Medido desde el router contra speed.cloudflare.com ("+fmt.Sprint(speedStreams)+" conexiones, "+speedPhase.String()+" por fase)") + "\n\n")

	switch {
	case m.spdRunning:
		fmt.Fprintf(&b, "%s  %s", cWarn.Render("Fase: "+s.Phase), mbps(s.Live))
		b.WriteString("\n\n")
	case s.Phase == "":
		b.WriteString(cDim.Render("Pulsa enter para empezar (tarda ~20 s y usa toda la línea).") + "\n")
	}
	if s.Phase != "" {
		if s.Latency > 0 {
			fmt.Fprintf(&b, "  Latencia   %6.1f ms   (jitter %.1f ms)\n", float64(s.Latency.Microseconds())/1000, float64(s.Jitter.Microseconds())/1000)
		}
		if s.Down > 0 {
			fmt.Fprintf(&b, "  Descarga   %s\n", cOK.Render(mbps(s.Down)))
		}
		if s.Up > 0 {
			fmt.Fprintf(&b, "  Subida     %s\n", cOK.Render(mbps(s.Up)))
		}
		if s.Err != nil {
			b.WriteString("\n" + cErr.Render("Error: "+clip(s.Err.Error(), m.w-8)) + "\n")
		}
	}
	b.WriteString("\n" + cDim.Render("Nota: el cifrado TLS consume CPU; en un ER-X las líneas de más de ~300 Mb/s\npueden salir limitadas por el router y no por la conexión."))
	return b.String()
}

func (m model) viewIfaces() string {
	if m.ifErr != nil {
		return cErr.Render("No se pudo leer " + netDevPath + ": " + m.ifErr.Error())
	}
	rs := m.ifs.sorted()
	var b strings.Builder
	if len(rs) == 0 {
		return cDim.Render("Midiendo… (la primera muestra tarda 2 s)")
	}
	b.WriteString(cHead.Render(fmt.Sprintf("%-10s %-9s %-12s %-12s %-10s %-10s %s", "IFACE", "ENLACE", "↓ BAJADA", "↑ SUBIDA", "TOT ↓", "TOT ↑", "ACTIVIDAD")) + "\n")
	from, to := m.listWindow(len(rs))
	for i := from; i < to; i++ {
		r := rs[i]
		line := fmt.Sprintf("%-10s %-9s %-12s %-12s %-10s %-10s %s",
			clip(r.Name, 10), clip(r.Link, 9), rate(r.RX), rate(r.TX), size(r.TotRX), size(r.TotTX), spark(r.Hist))
		b.WriteString(row(line, i == m.cursor[tabIfaces], m.w) + "\n")
	}
	b.WriteString("\n" + cHead.Render(fmt.Sprintf("%-10s %-9s %-12s %-12s %-10s %-10s", "TOTAL", "eth*", rate(m.ifs.TotRX), rate(m.ifs.TotTX), size(m.ifs.SumRX), size(m.ifs.SumTX))) + "\n")
	b.WriteString(cDim.Render("Totales = suma de eth0..ethN (un paquete enrutado cuenta al entrar y al salir).\nTOT = acumulado desde que abriste la app.") + "\n")
	return b.String()
}

func (m model) viewHostBW() string {
	var b strings.Builder
	if m.hbwErr != nil {
		return cErr.Render("No se pudo activar la contabilidad por equipo:") + "\n" + clip(m.hbwErr.Error(), m.w*2) +
			"\n\n" + cDim.Render("Requiere sudo sin contraseña para iptables (es lo normal en EdgeOS con el usuario admin).")
	}
	rs := m.hbw.sorted()
	if offloadOn(configPath) {
		b.WriteString(cWarn.Render("⚠ Hardware offload activo: el tráfico acelerado no se contabiliza y los valores saldrán bajos.") + "\n")
	}
	if len(rs) == 0 {
		return b.String() + cDim.Render("Preparando contadores… (se crean reglas de solo conteo en FORWARD, se borran al salir)")
	}
	b.WriteString(cHead.Render(fmt.Sprintf("%-16s %-20s %-12s %-12s %-10s %-10s", "IP", "NOMBRE", "↓ BAJADA", "↑ SUBIDA", "TOT ↓", "TOT ↑")) + "\n")
	from, to := m.listWindow(len(rs))
	for i := from; i < to; i++ {
		r := rs[i]
		name := r.Name
		if name == "" {
			name = "-"
		}
		line := fmt.Sprintf("%-16s %-20s %-12s %-12s %-10s %-10s", r.IP, clip(name, 20), rate(r.Down), rate(r.Up), size(r.TotDown), size(r.TotUp))
		b.WriteString(row(line, i == m.cursor[tabHostBW], m.w) + "\n")
	}
	b.WriteString("\n" + cDim.Render("Solo tráfico enrutado por el router (Internet y entre VLAN/redes). Acumulado desde que abriste esta pestaña."))
	return b.String()
}

var _ = time.Second
