package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// freeBytes devuelve el espacio libre (para usuarios normales) del sistema
// de ficheros que contiene `path`; si no existe, mira el directorio padre.
func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	for p := path; ; p = filepath.Dir(p) {
		err := syscall.Statfs(p, &st)
		if err == nil {
			return uint64(st.Bsize) * uint64(st.Bavail), nil
		}
		if !os.IsNotExist(err) || filepath.Dir(p) == p {
			return 0, err
		}
	}
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

// hasRoom dice si se puede escribir `need` bytes dejando al menos
// `minFree` libres. Si no se puede medir, se asume que sí.
func hasRoom(dir string, need, minFree uint64) bool {
	free, err := freeBytes(dir)
	return err != nil || free >= need+minFree
}

// trimLines deja solo las últimas `keep` líneas si el fichero supera maxBytes.
func trimLines(path string, maxBytes int64, keep int) error {
	if fileSize(path) <= maxBytes {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > keep {
			lines = lines[1:]
		}
	}
	f.Close()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
