#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ -x ./bin/golden-hour-server ]; then
  exec ./bin/golden-hour-server "$@"
fi
if command -v go >/dev/null 2>&1; then
  exec go run ./cmd/server "$@"
fi
for sdk in "$HOME"/.cache/golden-hour/toolchains/*/go/bin/go; do
  if [ -x "$sdk" ]; then
    exec "$sdk" run ./cmd/server "$@"
  fi
done
echo 'Install Go 1.26+ and run: go run ./cmd/server' >&2
exit 1
