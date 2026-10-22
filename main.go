package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// version se sobrescribe al compilar (-ldflags "-X main.version=...").
var version = "1.1.0"

const (
	tabHosts = iota
	tabLeases
	tabDNS
	tabSched
	tabSpeed
	tabIfaces
	tabHostBW
	tabHist
	tabAlerts
	numTabs
)

var tabTitles = []string{"1 Equipos", "2 DHCP", "3 DNS", "4 Reinicios", "5 Velocidad", "6 Interfaces", "7 Por equipo", "8 Histórico", "9 Avisos"}

var (
	cTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("62")).Padding(0, 1)
	cTabOn  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("62")).Padding(0, 1)
	cTabOff = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Padding(0, 1)
	cHead   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))
	cSel    = lipgloss.NewStyle().Reverse(true)
	cDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	cOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	cErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	cWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	cField  = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true)
)

// ---------- mensajes ----------

type dataMsg struct {
	leases   []Lease
	hosts    []Host
	tasks    []Task
	hostsErr error
	leaseErr error
	taskErr  error

	cfg     Config
	cfgErr  error
	alerts  []Alert
	collPID int
	collOK  bool
}
type histMsg struct {
	bs  []Bucket
	mod time.Time
	err error
}

// loadHist recarga el histórico si el fichero cambió desde `last`.
func loadHist(dir string, last time.Time, force bool) tea.Cmd {
	return func() tea.Msg {
		st, err := os.Stat(historyFile(dir))
		if err != nil {
			return histMsg{err: err}
		}
		if !force && st.ModTime().Equal(last) {
			return nil
		}
		bs, err := loadHistory(dir)
		return histMsg{bs, st.ModTime(), err}
	}
}

type dnsMsg struct{ res []DNSAttempt }
type opMsg struct {
	text string
	err  error
}
type tickMsg time.Time
type fastTickMsg time.Time
type speedTickMsg time.Time
type ifMsg struct {
	t     time.Time
	stats map[string]IfStat
	err   error
}
type acctMsg struct {
	t   time.Time
	cur map[string]HostCounters
	err error
}

func fastTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return fastTickMsg(t) })
}

func speedTick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return speedTickMsg(t) })
}

func readIfaces() tea.Msg {
	st, err := readNetDev(netDevPath)
	return ifMsg{time.Now(), st, err}
}

func pollAcct(ips []string) tea.Cmd {
	return func() tea.Msg {
		cur, err := acct.poll(ips)
		return acctMsg{time.Now(), cur, err}
	}
}

func loadData() tea.Msg {
	var d dataMsg
	d.leases, d.leaseErr = parseLeases(leasesPath)
	d.hosts, d.hostsErr = listHosts(d.leases)
	d.tasks, d.taskErr = listTasks(configPath)
	d.cfg, d.cfgErr = loadConfig()
	d.alerts = loadAlerts(d.cfg.DataDir, 100)
	d.collPID, d.collOK = collectorRunning(d.cfg.DataDir)
	return d
}

func tick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ---------- modelo ----------

type model struct {
	w, h int
	tab  int
	data dataMsg
	at   time.Time

	cursor [numTabs]int // posición en cada lista

	showAllLeases bool

	dnsName, dnsServer textinput.Model
	dnsFocus           int // 0 nombre, 1 servidor, 2 tipo
	dnsType            int
	dnsRunning         bool
	dnsRes             []DNSAttempt
	dnsRan             string

	// velocidad / ancho de banda
	spdRunning bool
	ifs        *netSampler
	ifErr      error
	hbw        *hostSampler
	hbwErr     error
	hbwBusy    bool

	// histórico
	hist      []Bucket
	histMod   time.Time
	histErr   error
	histSubj  string
	histDaily bool
	histTop   bool

	// reinicios
	form      bool
	fDay      int // -1 = todos los días, 0..6 = domingo..sábado
	fHour     int
	fMin      int
	fFocus    int // 0 día, 1 hora, 2 minuto
	fTyped    int
	confirmDe string
	status    string
	statusErr bool
	busy      bool
}

func newModel() model {
	n := textinput.New()
	n.SetValue("google.com")
	n.CharLimit = 253
	n.Width = 40
	s := textinput.New()
	s.SetValue(systemDNS())
	s.CharLimit = 80
	s.Width = 40
	n.Focus()
	return model{dnsName: n, dnsServer: s, fDay: 1, fHour: 4, fMin: 0, ifs: newNetSampler(), hbw: newHostSampler()}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(loadData, tick(), readIfaces, fastTick())
}

func (m model) editingText() bool { return m.tab == tabDNS && m.dnsFocus < 2 }

func (m model) listLen() int {
	switch m.tab {
	case tabHosts:
		return len(m.data.hosts)
	case tabLeases:
		return len(m.visibleLeases())
	case tabSched:
		return len(m.rebootTasks())
	case tabHist:
		return len(m.histRows())
	case tabAlerts:
		return len(m.data.alerts)
	case tabIfaces:
		return len(m.ifs.sorted())
	case tabHostBW:
		return len(m.hbw.sorted())
	}
	return 0
}

func (m model) visibleLeases() []Lease {
	if m.showAllLeases {
		return m.data.leases
	}
	var out []Lease
	for _, l := range m.data.leases {
		if l.Active() {
			out = append(out, l)
		}
	}
	return out
}

func (m model) rebootTasks() []Task {
	var out []Task
	for _, t := range m.data.tasks {
		if t.IsReboot() {
			out = append(out, t)
		}
	}
	return out
}

func (m model) setStatus(s string, isErr bool) model {
	m.status, m.statusErr = s, isErr
	return m
}

// ---------- update ----------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height

	case tickMsg:
		return m, tea.Batch(loadData, tick(), m.histCmd(false))

	case histMsg:
		m.histErr = msg.err
		if msg.err == nil {
			m.hist, m.histMod = msg.bs, msg.mod
		}

	case fastTickMsg:
		cmds := []tea.Cmd{readIfaces, fastTick()}
		if m.tab == tabHostBW && !m.hbwBusy {
			var ips []string
			for _, h := range m.data.hosts {
				ips = append(ips, h.IP)
			}
			m.hbwBusy = true
			cmds = append(cmds, pollAcct(ips))
		}
		return m, tea.Batch(cmds...)

	case ifMsg:
		m.ifErr = msg.err
		if msg.err == nil {
			m.ifs.update(msg.t, msg.stats)
		}

	case acctMsg:
		m.hbwBusy, m.hbwErr = false, msg.err
		if msg.err == nil {
			names := map[string]string{}
			for _, h := range m.data.hosts {
				names[h.IP] = h.Hostname
			}
			m.hbw.update(msg.t, msg.cur, names)
		}

	case speedTickMsg:
		if m.spdRunning {
			return m, speedTick()
		}

	case speedDoneMsg:
		m.spdRunning = false

	case dataMsg:
		m.data, m.at = msg, time.Now()
		for t := range m.cursor {
			// recorta cursores si la lista ha encogido
			save := m.tab
			m.tab = t
			if n := m.listLen(); m.cursor[t] >= n {
				m.cursor[t] = max(n-1, 0)
			}
			m.tab = save
		}

	case dnsMsg:
		m.dnsRunning, m.dnsRes = false, msg.res

	case opMsg:
		m.busy = false
		if msg.err != nil {
			m = m.setStatus(msg.text+": "+msg.err.Error(), true)
		} else {
			m = m.setStatus(msg.text, false)
		}
		return m, loadData

	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if m.busy {
		return m, nil
	}

	// el formulario de reinicios captura todo
	if m.tab == tabSched && m.form {
		return m.formKey(k)
	}
	if m.confirmDe != "" {
		if k == "s" || k == "S" || k == "y" || k == "Y" {
			name := m.confirmDe
			m.confirmDe, m.busy = "", true
			m = m.setStatus("Borrando "+name+"…", false)
			return m, func() tea.Msg {
				return opMsg{"Tarea " + name + " eliminada", deleteTask(name)}
			}
		}
		m.confirmDe = ""
		return m.setStatus("Cancelado", false), nil
	}

	// cambio de pestaña
	switch k {
	case "tab":
		return m.gotoTabCmd((m.tab + 1) % numTabs)
	case "shift+tab":
		return m.gotoTabCmd((m.tab + numTabs - 1) % numTabs)
	}
	if !m.editingText() {
		switch k {
		case "q":
			return m, tea.Quit
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			return m.gotoTabCmd(int(k[0] - '1'))
		case "right", "l":
			if m.tab != tabDNS {
				return m.gotoTabCmd((m.tab + 1) % numTabs)
			}
		case "left", "h":
			if m.tab != tabDNS {
				return m.gotoTabCmd((m.tab + numTabs - 1) % numTabs)
			}
		case "r":
			return m, tea.Batch(loadData, m.histCmd(true))
		}
	}

	switch m.tab {
	case tabHosts, tabLeases:
		m = m.moveCursor(k)
		if m.tab == tabLeases && k == "a" {
			m.showAllLeases = !m.showAllLeases
			m.cursor[tabLeases] = 0
		}
	case tabSched:
		m = m.moveCursor(k)
		switch k {
		case "a", "n":
			m.form, m.fFocus, m.fTyped = true, 0, 0
			m = m.setStatus("", false)
		case "d", "delete":
			if ts := m.rebootTasks(); len(ts) > 0 {
				m.confirmDe = ts[m.cursor[tabSched]].Name
			}
		}
	case tabDNS:
		return m.dnsKey(msg)
	case tabSpeed:
		if (k == "enter" || k == "s") && !m.spdRunning {
			m.spdRunning = true
			return m, tea.Batch(runSpeedTest, speedTick())
		}
	case tabIfaces, tabHostBW, tabAlerts:
		m = m.moveCursor(k)
	case tabHist:
		m = m.moveCursor(k)
		ss := subjects(m.hist)
		cur := indexOf(ss, m.histSubject(ss))
		switch k {
		case "s":
			if len(ss) > 0 {
				m.histSubj, m.cursor[tabHist] = ss[(cur+1)%len(ss)], 0
			}
		case "S":
			if len(ss) > 0 {
				m.histSubj, m.cursor[tabHist] = ss[(cur+len(ss)-1)%len(ss)], 0
			}
		case "m":
			m.histDaily, m.cursor[tabHist] = !m.histDaily, 0
		case "v":
			m.histTop, m.cursor[tabHist] = !m.histTop, 0
		}
	}
	return m, nil
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return 0
}

func (m model) histCmd(force bool) tea.Cmd {
	if m.tab != tabHist {
		return nil
	}
	dir := m.data.cfg.DataDir
	if dir == "" {
		dir = defaultConfig().DataDir
	}
	return loadHist(dir, m.histMod, force)
}

func (m model) gotoTabCmd(t int) (tea.Model, tea.Cmd) {
	m = m.gotoTab(t)
	return m, m.histCmd(false)
}

func (m model) gotoTab(t int) model {
	m.tab = t
	m.status = ""
	if t == tabDNS {
		m = m.dnsSetFocus(m.dnsFocus)
	}
	return m
}

func (m model) moveCursor(k string) model {
	n := m.listLen()
	c := &m.cursor[m.tab]
	switch k {
	case "up", "k":
		*c = max(*c-1, 0)
	case "down", "j":
		*c = min(*c+1, max(n-1, 0))
	case "pgup":
		*c = max(*c-10, 0)
	case "pgdown":
		*c = min(*c+10, max(n-1, 0))
	case "home", "g":
		*c = 0
	case "end", "G":
		*c = max(n-1, 0)
	}
	return m
}

// --- DNS ---

func (m model) dnsSetFocus(f int) model {
	m.dnsFocus = f
	m.dnsName.Blur()
	m.dnsServer.Blur()
	switch f {
	case 0:
		m.dnsName.Focus()
	case 1:
		m.dnsServer.Focus()
	}
	return m
}

func (m model) dnsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "down":
		return m.dnsSetFocus(min(m.dnsFocus+1, 2)), nil
	case "up":
		return m.dnsSetFocus(max(m.dnsFocus-1, 0)), nil
	case "enter":
		if m.dnsRunning {
			return m, nil
		}
		name := strings.TrimSpace(m.dnsName.Value())
		if name == "" {
			return m.setStatus("Escribe un nombre a resolver", true), nil
		}
		server := strings.TrimSpace(m.dnsServer.Value())
		if server == "" {
			server = systemDNS()
		}
		typ := dnsTypes[m.dnsType]
		m.dnsRunning, m.dnsRes, m.status = true, nil, ""
		m.dnsRan = fmt.Sprintf("%s %s @%s", typ, name, server)
		return m, func() tea.Msg { return dnsMsg{dnsTest(name, server, typ, 3)} }
	}
	if m.dnsFocus == 2 {
		switch msg.String() {
		case "left", "h":
			m.dnsType = (m.dnsType + len(dnsTypes) - 1) % len(dnsTypes)
		case "right", "l", " ":
			m.dnsType = (m.dnsType + 1) % len(dnsTypes)
		case "q":
			return m, tea.Quit
		case "r":
			return m, loadData
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			return m.gotoTabCmd(int(msg.String()[0] - '1'))
		}
		return m, nil
	}
	var cmd tea.Cmd
	if m.dnsFocus == 0 {
		m.dnsName, cmd = m.dnsName.Update(msg)
	} else {
		m.dnsServer, cmd = m.dnsServer.Update(msg)
	}
	return m, cmd
}

// --- formulario de reinicio ---

func (m model) formKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc":
		m.form = false
		return m, nil
	case "left", "shift+tab":
		m.fFocus, m.fTyped = (m.fFocus+2)%3, 0
	case "right", "tab":
		m.fFocus, m.fTyped = (m.fFocus+1)%3, 0
	case "up", "down":
		d := 1
		if k == "down" {
			d = -1
		}
		m.fTyped = 0
		switch m.fFocus {
		case 0:
			m.fDay = (m.fDay+1+d+8)%8 - 1 // rango -1..6
		case 1:
			m.fHour = (m.fHour + d + 24) % 24
		case 2:
			m.fMin = (m.fMin + d + 60) % 60
		}
	case "enter":
		m.form, m.busy = false, true
		dow, h, mi := m.fDay, m.fHour, m.fMin
		m = m.setStatus("Guardando en la configuración del router…", false)
		return m, func() tea.Msg {
			name, err := addReboot(dow, h, mi)
			return opMsg{"Reinicio programado (" + name + ")", err}
		}
	default:
		if len(k) == 1 && k[0] >= '0' && k[0] <= '9' && m.fFocus > 0 {
			d := int(k[0] - '0')
			v, lim := &m.fHour, 23
			if m.fFocus == 2 {
				v, lim = &m.fMin, 59
			}
			if m.fTyped == 1 && *v*10+d <= lim {
				*v = *v*10 + d
				m.fTyped = 2
			} else {
				*v, m.fTyped = d, 1
			}
		}
	}
	return m, nil
}

// ---------- vista ----------

func (m model) View() string {
	if m.w == 0 {
		return "Cargando…"
	}
	var b strings.Builder
	b.WriteString(cTitle.Render("ER-X TUI v"+version) + " ")
	b.WriteString(m.tabBar())
	b.WriteString("\n\n")

	body := ""
	switch m.tab {
	case tabHosts:
		body = m.viewHosts()
	case tabLeases:
		body = m.viewLeases()
	case tabDNS:
		body = m.viewDNS()
	case tabSched:
		body = m.viewSched()
	case tabSpeed:
		body = m.viewSpeed()
	case tabIfaces:
		body = m.viewIfaces()
	case tabHostBW:
		body = m.viewHostBW()
	case tabHist:
		body = m.viewHist()
	case tabAlerts:
		body = m.viewAlerts()
	}
	b.WriteString(body)

	// pie fijo en la última línea
	lines := strings.Count(b.String(), "\n")
	for ; lines < m.h-2; lines++ {
		b.WriteString("\n")
	}
	if m.status != "" {
		st := cOK
		if m.statusErr {
			st = cErr
		}
		b.WriteString(st.Render(clip(m.status, m.w-1)) + "\n")
	} else if n := m.recentAlerts(); n > 0 && m.tab != tabAlerts {
		b.WriteString(cWarn.Render(fmt.Sprintf("⚠ %d aviso(s) en las últimas 24 h — pestaña 9", n)) + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(cDim.Render(m.help()))
	return b.String()
}

func (m model) help() string {
	base := "tab/1-9 pestañas · ↑↓ mover · r refrescar · q salir"
	switch {
	case m.tab == tabLeases:
		return "a ver todas/solo activas · " + base
	case m.tab == tabDNS:
		return "↑↓ campo · ←→ tipo · enter probar · tab pestaña · ctrl+c salir"
	case m.tab == tabSched && m.form:
		return "←→ campo · ↑↓ cambiar valor (o teclea dígitos) · enter guardar · esc cancelar"
	case m.tab == tabHist:
		return "s/S equipo o interfaz · m horas/días · v serie/top · ↑↓ mover · q salir"
	case m.tab == tabSpeed:
		return "enter lanzar test · tab/1-9 pestañas · q salir"
	case m.tab == tabSched:
		return "a añadir · d borrar · " + base
	}
	return base
}

// listWindow devuelve el rango visible de una lista con scroll.
func (m model) listWindow(n int) (from, to int) {
	rows := max(m.h-9, 3)
	c := m.cursor[m.tab]
	from = max(min(c-rows/2, n-rows), 0)
	to = min(from+rows, n)
	return
}

func (m model) updated() string {
	if m.at.IsZero() {
		return ""
	}
	return cDim.Render("actualizado " + m.at.Format("15:04:05"))
}

func (m model) viewHosts() string {
	if m.data.hostsErr != nil {
		return cErr.Render("No se pudo leer la tabla ARP: " + m.data.hostsErr.Error())
	}
	hs := m.data.hosts
	var b strings.Builder
	fmt.Fprintf(&b, "%d equipos en la tabla ARP   %s\n\n", len(hs), m.updated())
	b.WriteString(cHead.Render(fmt.Sprintf("%-16s %-18s %-8s %-10s %s", "IP", "MAC", "IFACE", "ESTADO", "NOMBRE")) + "\n")
	from, to := m.listWindow(len(hs))
	for i := from; i < to; i++ {
		h := hs[i]
		name := h.Hostname
		if name == "" {
			name = cDim.Render("-")
		}
		line := fmt.Sprintf("%-16s %-18s %-8s %-10s %s", h.IP, h.MAC, h.Iface, h.State, name)
		b.WriteString(row(line, i == m.cursor[tabHosts], m.w) + "\n")
	}
	return b.String()
}

func (m model) viewLeases() string {
	if m.data.leaseErr != nil {
		return cErr.Render("No se pudo leer "+leasesPath+": "+m.data.leaseErr.Error()) +
			"\n" + cDim.Render("¿Está activo el servidor DHCP del router?")
	}
	ls := m.visibleLeases()
	mode := "activas"
	if m.showAllLeases {
		mode = "todas"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d cesiones (%s)   %s\n\n", len(ls), mode, m.updated())
	b.WriteString(cHead.Render(fmt.Sprintf("%-16s %-18s %-22s %-9s %s", "IP", "MAC", "NOMBRE", "ESTADO", "CADUCA")) + "\n")
	from, to := m.listWindow(len(ls))
	for i := from; i < to; i++ {
		l := ls[i]
		name := l.Hostname
		if name == "" {
			name = "-"
		}
		line := fmt.Sprintf("%-16s %-18s %-22s %-9s %s", l.IP, l.MAC, clip(name, 22), l.State, expiry(l))
		b.WriteString(row(line, i == m.cursor[tabLeases], m.w) + "\n")
	}
	return b.String()
}

func expiry(l Lease) string {
	if l.Ends.IsZero() {
		return "nunca"
	}
	d := time.Until(l.Ends)
	if d <= 0 {
		return "caducada " + l.Ends.Local().Format("02/01 15:04")
	}
	return fmt.Sprintf("en %s (%s)", dur(d), l.Ends.Local().Format("02/01 15:04"))
}

func dur(d time.Duration) string {
	d = d.Round(time.Minute)
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

func (m model) viewDNS() string {
	var b strings.Builder
	label := func(i int, s string) string {
		if m.dnsFocus == i {
			return cField.Render("▸ " + s)
		}
		return "  " + s
	}
	b.WriteString(label(0, fmt.Sprintf("%-10s", "Nombre")) + m.dnsName.View() + "\n")
	b.WriteString(label(1, fmt.Sprintf("%-10s", "Servidor")) + m.dnsServer.View() + "\n")
	var ts []string
	for i, t := range dnsTypes {
		if i == m.dnsType {
			ts = append(ts, cSel.Render(" "+t+" "))
		} else {
			ts = append(ts, " "+t+" ")
		}
	}
	b.WriteString(label(2, fmt.Sprintf("%-10s", "Tipo")) + strings.Join(ts, "") + "\n\n")

	switch {
	case m.dnsRunning:
		b.WriteString(cWarn.Render("Consultando " + m.dnsRan + " …"))
	case m.dnsRes != nil:
		b.WriteString(cHead.Render(m.dnsRan) + "\n")
		var total time.Duration
		ok := 0
		for i, r := range m.dnsRes {
			ms := fmt.Sprintf("%7.1f ms", float64(r.Elapsed.Microseconds())/1000)
			if r.Err != nil {
				b.WriteString(fmt.Sprintf(" #%d %s  %s\n", i+1, ms, cErr.Render(clip(r.Err.Error(), m.w-20))))
				continue
			}
			ok++
			total += r.Elapsed
			b.WriteString(fmt.Sprintf(" #%d %s  %s\n", i+1, ms, cOK.Render("OK")+" "+clip(strings.Join(r.Answers, ", "), m.w-24)))
		}
		fmt.Fprintf(&b, "\n%d/%d respuestas", ok, len(m.dnsRes))
		if ok > 0 {
			fmt.Fprintf(&b, ", media %.1f ms", float64(total.Microseconds())/1000/float64(ok))
		}
		b.WriteString("\n")
	default:
		b.WriteString(cDim.Render("Pulsa enter para lanzar 3 consultas seguidas.\nServidor vacío = el de /etc/resolv.conf."))
	}
	return b.String()
}

func (m model) viewSched() string {
	ts := m.rebootTasks()
	var b strings.Builder
	if m.data.taskErr != nil {
		return cErr.Render("No se pudo leer " + configPath + ": " + m.data.taskErr.Error())
	}
	fmt.Fprintf(&b, "%d reinicios programados   %s\n\n", len(ts), m.updated())

	if len(ts) == 0 {
		b.WriteString(cDim.Render("Ninguno. Pulsa a para añadir uno.") + "\n")
	} else {
		b.WriteString(cHead.Render(fmt.Sprintf("%-34s %s", "TAREA", "CUÁNDO")) + "\n")
		from, to := m.listWindow(len(ts))
		if m.form {
			to = min(to, from+4)
		}
		for i := from; i < to; i++ {
			line := fmt.Sprintf("%-34s %s", clip(ts[i].Name, 34), ts[i].Describe())
			b.WriteString(row(line, i == m.cursor[tabSched], m.w) + "\n")
		}
	}

	if m.confirmDe != "" {
		b.WriteString("\n" + cWarn.Render("¿Borrar "+m.confirmDe+"? (s/N)") + "\n")
	}
	if m.form {
		b.WriteString("\n" + cHead.Render("Nuevo reinicio semanal") + "\n")
		day := "Todos los días"
		if m.fDay >= 0 {
			day = dayNames[m.fDay]
		}
		f := func(i int, s string) string {
			if m.fFocus == i {
				return cSel.Render(" " + s + " ")
			}
			return " " + s + " "
		}
		fmt.Fprintf(&b, "  Día %s   Hora %s : %s\n", f(0, day), f(1, fmt.Sprintf("%02d", m.fHour)), f(2, fmt.Sprintf("%02d", m.fMin)))
		b.WriteString(cDim.Render("  "+Task{Cron: cronSpec(m.fDay, m.fHour, m.fMin)}.Describe()) + "\n")
	}
	return b.String()
}

func row(s string, selected bool, w int) string {
	s = clip(s, w-1)
	if selected {
		return cSel.Render(s + strings.Repeat(" ", max(w-1-lipgloss.Width(s), 0)))
	}
	return s
}

func clip(s string, w int) string {
	if w < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func main() {
	if len(os.Args) > 1 {
		if handled := runCLI(os.Args[1]); handled {
			return
		}
	}
	_, err := tea.NewProgram(newModel(), tea.WithAltScreen()).Run()
	acct.cleanup() // retira las reglas de contabilidad si se crearon
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
