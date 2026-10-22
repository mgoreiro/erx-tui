# erx-tui

TUI para EdgeRouter X (EdgeOS 2.x, MIPS). Un único binario estático de ~4 MB, sin dependencias en el router.

| Pestaña | Qué hace | De dónde lee |
|---|---|---|
| 1 Equipos | IP, MAC, interfaz, estado y nombre | `ip neigh` (o `/proc/net/arp`) + nombres de las cesiones |
| 2 Cesiones DHCP | IP, MAC, nombre, estado, caducidad (`a` = ver también las libres/caducadas) | `/var/run/dhcpd.leases` |
| 3 Prueba DNS | 3 consultas seguidas (A, AAAA, MX, TXT, NS, CNAME, PTR) contra el servidor que elijas, con tiempos | consulta UDP directa |
| 4 Reinicios | Lista, añade (`a`) y borra (`d`) reinicios semanales o diarios | task-scheduler de EdgeOS (`config.boot`) |
| 5 Velocidad | Latencia, jitter, descarga y subida de la WAN (`enter` lanza el test, ~20 s) | speed.cloudflare.com, medido desde el router |
| 6 Interfaces | Bajada/subida por interfaz, enlace y velocidad negociada, acumulado, gráfica de actividad y total de eth* | `/proc/net/dev`, `/sys/class/net` |
| 7 Por equipo | Bajada/subida actual y acumulada de cada equipo | contadores de `iptables` (ver abajo) |

**Consumo por equipo:** al abrir la pestaña 7 la app crea la cadena `ERXTUI` en `iptables`, enganchada al inicio de `FORWARD`, con dos reglas *sin objetivo* por equipo (`-s IP` y `-d IP`). Solo cuentan bytes y dejan pasar el paquete, así que no cambian el filtrado. Viven en memoria (desaparecen al reiniciar) y la app las borra al salir. Usa `sudo -n iptables`. Limitaciones: solo ve tráfico enrutado por el router, y si tienes **hardware offload (hwnat)** activado el tráfico acelerado no pasa por iptables y saldrá subestimado (la app avisa).

Los reinicios se guardan con `set system task-scheduler …` + `commit` + `save`, así que persisten tras reinicios y actualizaciones de firmware, y también se ven desde la CLI/GUI de EdgeOS.

## Instalar

```sh
./build.sh                      # genera dist/erx-tui-mipsle
./install.sh 192.168.1.1 ubnt   # copia a /config/user-data/erx-tui
ssh -t ubnt@192.168.1.1 /config/user-data/erx-tui
```

Usa un usuario del grupo `vyattacfg` (el administrador normal lo es) y **no lo lances con `sudo`**: la pestaña de reinicios aplica cambios con `sg vyattacfg`.

## Teclas

`tab`/`1-7` cambiar pestaña · `↑↓` mover · `r` refrescar · `q` salir. En DNS: `↑↓` campo, `←→` tipo, `enter` probar. En Reinicios: `a` añadir (←→ campo, ↑↓ o dígitos para el valor, enter guarda), `d` borrar.

## Desarrollo

`go test` usa los ficheros de `testdata/`. Se puede probar la interfaz en un Mac apuntando a ellos:

```sh
ERX_LEASES=testdata/dhcpd.leases ERX_CONFIG=testdata/config.boot ERX_ARP=testdata/arp go run .
```

(Añadir/borrar reinicios solo funciona en un router real.)

## Licencia

GPL-3.0 o posterior. Ver [LICENSE](LICENSE).
