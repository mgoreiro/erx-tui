# Changelog

## 1.1.0

- Colector en segundo plano (`--collect`, `--install-service`, `--remove-service`, `--status`) que guarda el tráfico por hora de interfaces y equipos.
- Pestaña 8 Histórico: serie por horas/días, por interfaz o equipo, y ranking de equipos.
- Pestaña 9 Avisos: umbrales sostenidos por interfaz y equipo y cuota diaria por equipo; salida a syslog, fichero y webhook opcional.
- Barra de pestañas compacta en terminales estrechas.

## 1.0.0 — 2026-10-22

Primera versión.

- Equipos conectados (ARP/`ip neigh` + nombres DHCP).
- Cesiones DHCP con caducidad.
- Prueba DNS (A, AAAA, MX, TXT, NS, CNAME, PTR) con tiempos.
- Reinicios programados mediante el task-scheduler de EdgeOS.
- Test de velocidad (latencia, jitter, descarga, subida).
- Consumo de ancho de banda por interfaz y total.
- Consumo de ancho de banda por equipo (contadores de `iptables`).
