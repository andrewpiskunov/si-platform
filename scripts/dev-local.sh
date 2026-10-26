#!/usr/bin/env bash
# Start this checkout only. Never reuse or terminate a process on an occupied port.
set -euo pipefail
project_root="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$project_root"
web_port="${WEB_PORT:-5175}"
api_port="${API_PORT:-8082}"
: "${DATABASE_URL:?Set DATABASE_URL for the local database (see README.md)}"
for port in "$web_port" "$api_port"; do
  if ! [[ "$port" =~ ^[0-9]+$ ]] || (( port < 1024 || port > 65535 )); then
    printf 'Invalid port: %s\n' "$port" >&2; exit 1
  fi
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    printf 'Port %s is occupied. Its process was not changed. Choose WEB_PORT/API_PORT or stop your own previous launch.\n' "$port" >&2
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >&2
    exit 1
  fi
done
if [[ "$web_port" == "$api_port" ]]; then echo 'WEB_PORT and API_PORT must differ.' >&2; exit 1; fi
node -e 'const [a,b]=process.versions.node.split(".").map(Number);if(a<22||(a===22&&b<17)){console.error("Node >=22.17 required");process.exit(1)}'
mkdir -p .local/bin
printf 'Checkout: %s\nRevision: %s\nWeb: http://localhost:%s\nAPI: http://127.0.0.1:%s\n' "$project_root" "$(git rev-parse --short HEAD)" "$web_port" "$api_port"
go run ./cmd/migrate
go build -o .local/bin/server-dev ./cmd/server
# Local verification/reset emails stay on disk unless SMTP was explicitly configured.
if [[ -z "${SMTP_ADDR:-}" ]]; then export MAIL_DIRECTORY="${MAIL_DIRECTORY:-$project_root/.local/mail}"; fi
api_pid=''; web_pid=''
cleanup() {
  trap - EXIT INT TERM
  [[ -z "$web_pid" ]] || kill "$web_pid" 2>/dev/null || true
  [[ -z "$api_pid" ]] || kill "$api_pid" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM
LISTEN_ADDR="127.0.0.1:$api_port" ALLOWED_ORIGIN="http://localhost:$web_port" DATA_DIR="${DATA_DIR:-.local/data}" MEDIA_DISABLED=true AI_PROVIDER=disabled .local/bin/server-dev &
api_pid=$!
(cd web; export API_ORIGIN="http://127.0.0.1:$api_port"; exec node node_modules/vite/bin/vite.js --host 127.0.0.1 --port "$web_port" --strictPort) &
web_pid=$!
while kill -0 "$api_pid" 2>/dev/null && kill -0 "$web_pid" 2>/dev/null; do sleep 1; done
exit 1
