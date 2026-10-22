#!/bin/sh
# Uso: ./install.sh 192.168.1.1 [usuario]
# Copia el binario a /config/user-data, que sobrevive a reinicios y a
# actualizaciones de firmware.
set -e
cd "$(dirname "$0")"
HOST="${1:?uso: $0 <ip-del-router> [usuario]}"
USER="${2:-ubnt}"
[ -f dist/erx-tui-mipsle ] || ./build.sh
scp -O dist/erx-tui-mipsle "$USER@$HOST:/tmp/erx-tui" 2>/dev/null || scp dist/erx-tui-mipsle "$USER@$HOST:/tmp/erx-tui"
ssh "$USER@$HOST" 'mkdir -p /config/user-data && mv /tmp/erx-tui /config/user-data/erx-tui && chmod +x /config/user-data/erx-tui'
echo "Instalado. Ejecuta:  ssh -t $USER@$HOST /config/user-data/erx-tui"
echo "Histórico y avisos (una vez):  ssh -t $USER@$HOST sudo /config/user-data/erx-tui --install-service"
