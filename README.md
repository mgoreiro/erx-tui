# erx-tui

TUI para EdgeRouter X (EdgeOS 2.x, MIPS). Un único binario estático de ~8 MB, sin dependencias en el router.

| Pestaña | Qué hace | De dónde lee |
|---|---|---|
| 1 Equipos | IP, MAC, interfaz, estado y nombre | `ip neigh` (o `/proc/net/arp`) + nombres de las cesiones |
| 2 Cesiones DHCP | IP, MAC, nombre, estado, caducidad (`a` = ver también las libres/caducadas) | `/var/run/dhcpd.leases` |
| 3 Prueba DNS | 3 consultas seguidas (A, AAAA, MX, TXT, NS, CNAME, PTR) contra el servidor que elijas, con tiempos | consulta UDP directa |
| 4 Reinicios | Lista, añade (`a`) y borra (`d`) reinicios semanales o diarios | task-scheduler de EdgeOS (`config.boot`) |
| 5 Velocidad | Latencia, jitter, descarga y subida de la WAN (`enter` lanza el test, ~20 s) | speed.cloudflare.com, medido desde el router |
| 6 Interfaces | Bajada/subida por interfaz, enlace y velocidad negociada, acumulado, gráfica de actividad y total de eth* | `/proc/net/dev`, `/sys/class/net` |
| 7 Por equipo | Bajada/subida actual y acumulada de cada equipo | contadores de `iptables` (ver abajo) |
| 8 Histórico | Tráfico por hora (24 h) o por día (30 días) de una interfaz o de un equipo, y ranking de equipos (`s` cambia de interfaz/equipo, `m` horas/días, `v` serie/top) | datos del colector |
| 9 Avisos | Umbrales configurados y avisos recientes | `alerts.jsonl` del colector |

**Consumo por equipo:** al abrir la pestaña 7 la app crea la cadena `ERXTUI` en `iptables`, enganchada al inicio de `FORWARD`, con dos reglas *sin objetivo* por equipo (`-s IP` y `-d IP`). Solo cuentan bytes y dejan pasar el paquete, así que no cambian el filtrado. Viven en memoria (desaparecen al reiniciar) y la app las borra al salir. Usa `sudo -n iptables`. Limitaciones: solo ve tráfico enrutado por el router, y si tienes **hardware offload (hwnat)** activado el tráfico acelerado no pasa por iptables y saldrá subestimado (la app avisa).

## Histórico y avisos (colector)

Las pestañas 8 y 9 necesitan el **colector**, un proceso en segundo plano (`erx-tui --collect`) que mide cada 60 s el tráfico de las interfaces y de cada equipo, y guarda totales por hora en `/config/user-data/erx-tui-data/` (se conserva entre reinicios y firmware; 90 días por defecto). Escribe cada 30 min y en formato solo-añadir, para gastar poca flash.

```sh
sudo /config/user-data/erx-tui --install-service   # arranca ya y en cada arranque del router
sudo /config/user-data/erx-tui --remove-service    # lo detiene y quita; el histórico se conserva
/config/user-data/erx-tui --status
```

El arranque usa `/config/scripts/post-config.d/` (se ejecuta como root en EdgeOS). El colector usa su propia cadena `ERXTUID` de `iptables`, independiente de la de la pestaña 7, con las mismas limitaciones (offload, solo tráfico enrutado).

**Avisos.** Copia `erx-tui.example.json` a `/config/user-data/erx-tui.json` y ajusta los umbrales (sin ese fichero los avisos están desactivados y el colector solo guarda histórico):

| Campo | Significado |
|---|---|
| `alerts.iface_mbps` | `{"eth0": 400}`: aviso si esa interfaz supera 400 Mb/s (en cualquier sentido) durante `sustained_minutes` seguidos |
| `alerts.host_mbps` | lo mismo, por equipo |
| `alerts.host_gb_day` | aviso (una vez al día) cuando un equipo supera esos GB entre bajada y subida |
| `webhook_url` | opcional: se hace un `POST` JSON (`{"text": …}`) a esa URL con cada aviso |

**Uso de la flash.** El colector está pensado para no llenar el router: no guarda ceros, escribe cada 30 min y compacta una vez al día. Límites (configurables en el mismo JSON): `history_max_mb` (8 por defecto; si se supera se descartan las horas más antiguas), `retention_days` (90) y `min_free_mb` (20; si queda menos libre deja de escribir y lo registra, y reanuda al liberar espacio). El fichero de avisos se recorta solo a las últimas 200 líneas y un mismo aviso sostenido no se repite antes de 30 min. La pestaña 8 muestra lo que ocupa el histórico y lo que queda libre. Con 15 interfaces y 20 equipos el histórico completo ronda 3-4 MB.

Cada aviso se guarda en `alerts.jsonl`, se envía a syslog (`logger -t erx-tui`) y aparece como banner en la interfaz. Tras editar el fichero, reinicia el colector (`--remove-service` e `--install-service`).

Los reinicios se guardan con `set system task-scheduler …` + `commit` + `save`, así que persisten tras reinicios y actualizaciones de firmware, y también se ven desde la CLI/GUI de EdgeOS.

## Instalar

```sh
./build.sh                      # genera dist/erx-tui-mipsle
./install.sh 192.168.1.1 ubnt   # copia a /config/user-data/erx-tui
ssh -t ubnt@192.168.1.1 /config/user-data/erx-tui
```

Usa un usuario del grupo `vyattacfg` (el administrador normal lo es) y **no lo lances con `sudo`**: la pestaña de reinicios aplica cambios con `sg vyattacfg`.

## Teclas

`tab`/`1-9` cambiar pestaña · `↑↓` mover · `r` refrescar · `q` salir. En DNS: `↑↓` campo, `←→` tipo, `enter` probar. En Reinicios: `a` añadir (←→ campo, ↑↓ o dígitos para el valor, enter guarda), `d` borrar.

## Desarrollo

`go test` usa los ficheros de `testdata/`. Se puede probar la interfaz en un Mac apuntando a ellos:

```sh
ERX_LEASES=testdata/dhcpd.leases ERX_CONFIG=testdata/config.boot ERX_ARP=testdata/arp go run .
```

(Añadir/borrar reinicios solo funciona en un router real.)

## Licencia

GPL-3.0 o posterior. Ver [LICENSE](LICENSE).
