#!/bin/sh
# Compila para el EdgeRouter X (MT7621, mipsel). Necesita Go >= 1.21.
set -e
cd "$(dirname "$0")"
mkdir -p dist
GOOS=linux GOARCH=mipsle GOMIPS=softfloat CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w" -o dist/erx-tui-mipsle .
ls -lh dist/erx-tui-mipsle
