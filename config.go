package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Fichero de configuración opcional (JSON). Si no existe, el colector
// funciona con los valores por defecto y los avisos quedan desactivados.
//
//	{
//	  "interval_seconds": 60,
//	  "retention_days": 90,
//	  "webhook_url": "",
//	  "alerts": {
//	    "sustained_minutes": 5,
//	    "iface_mbps": {"eth0": 400},
//	    "host_mbps": 100,
//	    "host_gb_day": 50
//	  }
//	}
type Config struct {
	DataDir       string    `json:"data_dir"`
	IntervalSec   int       `json:"interval_seconds"`
	RetentionDays int       `json:"retention_days"`
	HistoryMaxMB  int       `json:"history_max_mb"` // tope del fichero de histórico
	MinFreeMB     int       `json:"min_free_mb"`    // no escribir si queda menos libre
	WebhookURL    string    `json:"webhook_url"`
	Alerts        AlertsCfg `json:"alerts"`
}

type AlertsCfg struct {
	SustainedMinutes int                `json:"sustained_minutes"`
	IfaceMbps        map[string]float64 `json:"iface_mbps"` // umbral sostenido por interfaz (cualquier sentido)
	HostMbps         float64            `json:"host_mbps"`  // umbral sostenido por equipo (cualquier sentido)
	HostGBDay        float64            `json:"host_gb_day"`
}

var confPath = envOr("ERX_TUI_CONF", "/config/user-data/erx-tui.json")

func defaultConfig() Config {
	return Config{
		DataDir:       envOr("ERX_TUI_DATA", "/config/user-data/erx-tui-data"),
		IntervalSec:   60,
		RetentionDays: 90,
		HistoryMaxMB:  8,
		MinFreeMB:     20,
		Alerts:        AlertsCfg{SustainedMinutes: 5},
	}
}

// loadConfig devuelve los valores por defecto si el fichero no existe;
// si existe pero es inválido devuelve además el error.
func loadConfig() (Config, error) {
	c := defaultConfig()
	b, err := os.ReadFile(confPath)
	if err != nil {
		return c, nil
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return defaultConfig(), err
	}
	if c.DataDir == "" {
		c.DataDir = defaultConfig().DataDir
	}
	if c.IntervalSec < 5 {
		c.IntervalSec = 60
	}
	if c.RetentionDays < 1 {
		c.RetentionDays = 90
	}
	if c.HistoryMaxMB < 1 {
		c.HistoryMaxMB = 8
	}
	if c.MinFreeMB < 1 {
		c.MinFreeMB = 20
	}
	if c.Alerts.SustainedMinutes < 1 {
		c.Alerts.SustainedMinutes = 5
	}
	return c, nil
}

func (a AlertsCfg) enabled() bool {
	return len(a.IfaceMbps) > 0 || a.HostMbps > 0 || a.HostGBDay > 0
}

func historyFile(dir string) string { return filepath.Join(dir, "history.jsonl") }
func alertsFile(dir string) string  { return filepath.Join(dir, "alerts.jsonl") }
func pidFile(dir string) string     { return filepath.Join(dir, "collector.pid") }
