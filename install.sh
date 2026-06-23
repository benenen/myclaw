#!/usr/bin/env bash
#
# install.sh — build & install myclaw + its MCP servers, and (optionally) run
# myclaw as a managed service under systemd or supervisor.
#
# Usage:
#   ./install.sh [options]
#
# Options:
#   --prefix DIR        install binaries into DIR/bin           (default: /usr/local)
#   --service KIND      service manager: systemd|supervisor|none|auto  (default: auto)
#   --user USER         run the service as this user            (default: current user)
#   --env-file FILE     env file holding CHANNEL_MASTER_KEY etc (default: <data>/myclaw.env)
#   --http-addr ADDR    CHANNEL_HTTP_ADDR for the service       (default: :8080)
#   --no-service        alias for --service none
#   --no-build          skip building (install/serviceize existing binaries)
#   -h, --help          show this help
#
# Environment:
#   CHANNEL_MASTER_KEY  base64-encoded 32-byte AES key. If unset, a key in the
#                       env-file is reused, otherwise a new one is generated.
#
set -euo pipefail

# ---- defaults -------------------------------------------------------------
PREFIX="/usr/local"
SERVICE="auto"
RUN_USER="$(id -un)"
HTTP_ADDR=":8080"
DO_BUILD=1
ENV_FILE=""
MCPS=(a2a boo echo ping)   # built as bin/mcp-<x> → installed as <x>-mcp

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[warn]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }

# ---- args -----------------------------------------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)     PREFIX="$2"; shift 2 ;;
    --service)    SERVICE="$2"; shift 2 ;;
    --user)       RUN_USER="$2"; shift 2 ;;
    --env-file)   ENV_FILE="$2"; shift 2 ;;
    --http-addr)  HTTP_ADDR="$2"; shift 2 ;;
    --no-service) SERVICE="none"; shift ;;
    --no-build)   DO_BUILD=0; shift ;;
    -h|--help)    sed -n '2,30p' "$0"; exit 0 ;;
    *)            die "unknown option: $1 (try --help)" ;;
  esac
done

BINDIR="$PREFIX/bin"
DATA_DIR="$(eval echo "~$RUN_USER")/.myclaw"     # server defaults data under ~/.myclaw
[ -n "$ENV_FILE" ] || ENV_FILE="$DATA_DIR/myclaw.env"

# sudo helper: only escalate when we actually lack write permission
SUDO=""
need_sudo() { [ ! -w "$1" ] && [ "$(id -u)" -ne 0 ]; }
if need_sudo "$BINDIR" || need_sudo "/etc/systemd/system" 2>/dev/null; then
  command -v sudo >/dev/null 2>&1 && SUDO="sudo" || warn "no sudo; writes to system dirs may fail"
fi

# ---- prereqs --------------------------------------------------------------
command -v go >/dev/null 2>&1 || die "go is not installed / not on PATH"
[ -f "$SCRIPT_DIR/go.mod" ] || die "run from the myclaw repo root (go.mod not found)"

# ---- build ----------------------------------------------------------------
if [ "$DO_BUILD" -eq 1 ]; then
  log "Building myclaw server + MCP servers (this may take a moment)…"
  ( cd "$SCRIPT_DIR" && go build -o "bin/myclaw" . )
  for m in "${MCPS[@]}"; do
    ( cd "$SCRIPT_DIR" && go build -o "bin/mcp-$m" "./mcps/$m" )
  done
else
  log "Skipping build (--no-build)"
fi

# ---- install binaries -----------------------------------------------------
log "Installing binaries into $BINDIR"
$SUDO install -d -m 0755 "$BINDIR"
$SUDO install -m 0755 "$SCRIPT_DIR/bin/myclaw" "$BINDIR/myclaw"
for m in "${MCPS[@]}"; do
  # deployed name convention: <name>-mcp (e.g. a2a-mcp) — matches bot configs
  $SUDO install -m 0755 "$SCRIPT_DIR/bin/mcp-$m" "$BINDIR/$m-mcp"
  printf '    %-14s -> %s\n' "mcp-$m" "$BINDIR/$m-mcp"
done

# ---- master key + env file ------------------------------------------------
install -d -m 0700 "$DATA_DIR" 2>/dev/null || $SUDO install -d -m 0700 -o "$RUN_USER" "$DATA_DIR"

resolve_master_key() {
  if [ -n "${CHANNEL_MASTER_KEY:-}" ]; then echo "$CHANNEL_MASTER_KEY"; return; fi
  if [ -f "$ENV_FILE" ]; then
    local k; k="$(grep -E '^CHANNEL_MASTER_KEY=' "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2-)"
    [ -n "$k" ] && { echo "$k"; return; }
  fi
  command -v openssl >/dev/null 2>&1 || die "CHANNEL_MASTER_KEY unset and openssl missing to generate one"
  openssl rand -base64 32
}
MASTER_KEY="$(resolve_master_key)"

if [ ! -f "$ENV_FILE" ]; then
  log "Writing env file $ENV_FILE"
  umask 077
  cat > "$ENV_FILE" <<EOF
# myclaw service environment — keep this file private (contains the master key).
CHANNEL_MASTER_KEY=$MASTER_KEY
CHANNEL_HTTP_ADDR=$HTTP_ADDR
# CHANNEL_SQLITE_PATH=$DATA_DIR/myclaw.db
# CHANNEL_MCP_URL=http://127.0.0.1$HTTP_ADDR/mcp
EOF
  chmod 0600 "$ENV_FILE"
  warn "A master key was written to $ENV_FILE. Back it up — losing it makes stored credentials undecryptable."
else
  log "Reusing existing env file $ENV_FILE"
fi

# ---- service manager detection --------------------------------------------
if [ "$SERVICE" = "auto" ]; then
  if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then SERVICE="systemd"
  elif command -v supervisorctl >/dev/null 2>&1; then SERVICE="supervisor"
  else SERVICE="none"; fi
fi

case "$SERVICE" in
  systemd)
    UNIT="/etc/systemd/system/myclaw.service"
    log "Installing systemd unit $UNIT (user=$RUN_USER)"
    $SUDO tee "$UNIT" >/dev/null <<EOF
[Unit]
Description=myclaw channel management service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$RUN_USER
EnvironmentFile=$ENV_FILE
ExecStart=$BINDIR/myclaw
WorkingDirectory=$DATA_DIR
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
    $SUDO systemctl daemon-reload
    $SUDO systemctl enable myclaw >/dev/null 2>&1 || true
    $SUDO systemctl restart myclaw
    log "myclaw is running under systemd. Status: systemctl status myclaw | Logs: journalctl -u myclaw -f"
    ;;

  supervisor)
    CONF="/etc/supervisor/conf.d/myclaw.conf"
    log "Installing supervisor program $CONF (user=$RUN_USER)"
    # supervisor reads the env inline; strip comments/blank lines from the env file.
    ENV_INLINE="$(grep -E '^[A-Z_]+=' "$ENV_FILE" | paste -sd, -)"
    $SUDO install -d -m 0755 "$(dirname "$CONF")"
    $SUDO tee "$CONF" >/dev/null <<EOF
[program:myclaw]
command=$BINDIR/myclaw
directory=$DATA_DIR
user=$RUN_USER
environment=$ENV_INLINE
autostart=true
autorestart=true
startsecs=2
stopsignal=TERM
stdout_logfile=/var/log/myclaw.out.log
stderr_logfile=/var/log/myclaw.err.log
EOF
    $SUDO supervisorctl reread
    $SUDO supervisorctl update
    $SUDO supervisorctl restart myclaw 2>/dev/null || $SUDO supervisorctl start myclaw
    log "myclaw is running under supervisor. Status: supervisorctl status myclaw"
    ;;

  none)
    log "No service manager configured. Run manually with:"
    printf '    env $(grep -vE "^#" %q | xargs) %s\n' "$ENV_FILE" "$BINDIR/myclaw"
    ;;

  *) die "unknown --service value: $SERVICE (systemd|supervisor|none|auto)" ;;
esac

log "Done. Binaries in $BINDIR; config in $ENV_FILE."
