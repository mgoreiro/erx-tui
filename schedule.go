package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Los reinicios se guardan en el task-scheduler de EdgeOS, así sobreviven
// a reinicios y a actualizaciones de firmware (viven en config.boot):
//
//	system { task-scheduler { task NOMBRE {
//	    crontab-spec "0 4 * * 1"
//	    executable { path /sbin/reboot }
//	} } }

const rebootPath = "/sbin/reboot"

var dayNames = []string{"Domingo", "Lunes", "Martes", "Miércoles", "Jueves", "Viernes", "Sábado"}

type Task struct {
	Name string
	Cron string
	Path string
}

func (t Task) IsReboot() bool { return strings.HasSuffix(t.Path, "/reboot") }

// Describe convierte "0 4 * * 1" en "Lunes a las 04:00".
func (t Task) Describe() string {
	f := strings.Fields(t.Cron)
	if len(f) != 5 || f[2] != "*" || f[3] != "*" {
		return t.Cron
	}
	m, err1 := strconv.Atoi(f[0])
	h, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return t.Cron
	}
	when := fmt.Sprintf("a las %02d:%02d", h, m)
	if f[4] == "*" {
		return "Todos los días " + when
	}
	if d, err := strconv.Atoi(f[4]); err == nil && d >= 0 && d <= 7 {
		return "Cada " + strings.ToLower(dayNames[d%7]) + " " + when
	}
	return t.Cron
}

// ---------- lectura de config.boot ----------

type node struct {
	name     string
	args     string
	children []*node
}

func parseConfig(data string) *node {
	root := &node{}
	stack := []*node{root}
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "//") {
			continue
		}
		if line == "}" {
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		opens := strings.HasSuffix(line, "{")
		line = strings.TrimSpace(strings.TrimSuffix(line, "{"))
		name, args, _ := strings.Cut(line, " ")
		n := &node{name: name, args: strings.Trim(strings.TrimSpace(args), `"`)}
		top := stack[len(stack)-1]
		top.children = append(top.children, n)
		if opens {
			stack = append(stack, n)
		}
	}
	return root
}

func (n *node) child(name string) *node {
	if n == nil {
		return nil
	}
	for _, c := range n.children {
		if c.name == name {
			return c
		}
	}
	return nil
}

func listTasks(path string) ([]Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ts := parseConfig(string(data)).child("system").child("task-scheduler")
	var out []Task
	if ts == nil {
		return out, nil
	}
	for _, c := range ts.children {
		if c.name != "task" {
			continue
		}
		t := Task{Name: c.args}
		if n := c.child("crontab-spec"); n != nil {
			t.Cron = n.args
		} else if n := c.child("interval"); n != nil {
			t.Cron = "cada " + n.args
		}
		t.Path = c.child("executable").child("path").argsOr("")
		out = append(out, t)
	}
	return out, nil
}

func (n *node) argsOr(def string) string {
	if n == nil {
		return def
	}
	return n.args
}

// ---------- escritura vía CLI de EdgeOS ----------

var safeName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)

// taskName: dow -1 = todos los días.
func taskName(dow, h, m int) string {
	d := "diario"
	if dow >= 0 {
		d = []string{"dom", "lun", "mar", "mie", "jue", "vie", "sab"}[dow]
	}
	return fmt.Sprintf("tui-reboot-%s-%02d%02d", d, h, m)
}

func cronSpec(dow, h, m int) string {
	d := "*"
	if dow >= 0 {
		d = strconv.Itoa(dow)
	}
	return fmt.Sprintf("%d %d * * %s", m, h, d)
}

func addReboot(dow, h, m int) (string, error) {
	if h < 0 || h > 23 || m < 0 || m > 59 || dow < -1 || dow > 6 {
		return "", fmt.Errorf("hora o día fuera de rango")
	}
	name := taskName(dow, h, m)
	return name, runConfig(
		fmt.Sprintf("set system task-scheduler task %s crontab-spec '%s'", name, cronSpec(dow, h, m)),
		fmt.Sprintf("set system task-scheduler task %s executable path %s", name, rebootPath),
	)
}

func deleteTask(name string) error {
	if !safeName.MatchString(name) {
		return fmt.Errorf("nombre de tarea no válido: %q", name)
	}
	return runConfig("delete system task-scheduler task " + name)
}

// runConfig ejecuta comandos de configuración y hace commit + save.
// Requiere ejecutarse como usuario del grupo vyattacfg (admin lo es).
func runConfig(cmds ...string) error {
	script := "#!/bin/vbash\nsource /opt/vyatta/etc/functions/script-template\nconfigure\n" +
		strings.Join(cmds, "\n") + "\ncommit\nsave\nexit\n"

	f, err := os.CreateTemp("", "erx-tui-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		return err
	}
	f.Close()

	out, err := exec.Command("sg", "vyattacfg", "-c", "/bin/vbash "+f.Name()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	// vbash devuelve 0 aunque el commit falle; detectarlo por el texto.
	if s := string(out); strings.Contains(s, "Commit failed") || strings.Contains(s, "Set failed") || strings.Contains(s, "Delete failed") {
		return fmt.Errorf("%s", strings.TrimSpace(s))
	}
	return nil
}
