package main

import (
	"fmt"
	"os"
)

const usage = `erx-tui — herramienta de terminal para EdgeRouter X

  erx-tui                  abre la interfaz
  erx-tui --collect        colector en segundo plano (histórico y avisos)
  erx-tui --install-service  arranca el colector y lo registra para el arranque (usar con sudo)
  erx-tui --remove-service   detiene el colector y quita el arranque (usar con sudo)
  erx-tui --status         estado del colector
  erx-tui --version`

// runCLI atiende los subcomandos sin interfaz; devuelve true si lo era.
func runCLI(arg string) bool {
	var err error
	switch arg {
	case "--collect":
		err = runCollector()
	case "--install-service":
		err = installService()
	case "--remove-service":
		err = removeService()
	case "--status":
		cfg, _ := loadConfig()
		if pid, ok := collectorRunning(cfg.DataDir); ok {
			fmt.Printf("colector activo (pid %d), datos en %s\n", pid, cfg.DataDir)
		} else {
			fmt.Println("colector parado")
		}
	case "-v", "--version":
		fmt.Println("erx-tui", version)
	case "-h", "--help":
		fmt.Println(usage)
	default:
		return false
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return true
}
