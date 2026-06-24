#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "${SCRIPT_DIR}"

COMPOSE_DOCKER="docker-compose.local.yml"

BINARY="./new-api"
FRONTEND_DIR="./web/default"
CLASSIC_DIR="./web/classic"
PID_BACKEND="/tmp/new-api-backend.pid"
PID_FRONTEND="/tmp/new-api-frontend.pid"
PID_TCPDUMP="/tmp/new-api-tcpdump.pid"
LOG_DIR="${SCRIPT_DIR}/logs"
LOG_BACKEND="${LOG_DIR}/backend.log"
LOG_FRONTEND="${LOG_DIR}/frontend.log"
CAPTURE_DIR="${SCRIPT_DIR}/logs/capture"
CAPTURE_PCAP="${CAPTURE_DIR}/capture.pcap"
CAPTURE_TXT="${CAPTURE_DIR}/capture.txt"
CAPTURE_REPORT="${CAPTURE_DIR}/report.txt"

RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${CYAN}[$(date +%H:%M:%S)]${NC} $*"; }
ok()   { echo -e "${GREEN}[OK]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }

pid_alive() { [[ -f "$1" ]] && kill -0 "$(cat "$1")" 2>/dev/null; }

load_env() {
  if [[ -f .env.local ]]; then
    log "Loading .env.local..."
    set -a; source .env.local; set +a
  fi
}

build_frontend() {
  local dir="$1" name="$2"
  if [[ ! -d "${dir}" ]]; then
    warn "${name} dir '${dir}' not found, skipping"
    return
  fi
  log "Building ${name} frontend..."
  (cd "${dir}" && bun install && bun run build) || {
    err "${name} build failed"
    return 1
  }
  ok "${name} built"
}

usage() {
  cat <<EOF
Usage: ./run.sh <env> <command>

Environments:
  local     Dev environment on bare metal — builds frontend + go build, no Docker
  docker    Docker environment (docker-compose.local.yml) — full stack in containers

Commands (local):
  start       Build (if needed) and start backend in background
  stop        Kill backend and frontend processes
  restart     stop + start
  build       Build frontends (bun build) + Go binary
  backend     Start backend only (no frontend)
  frontend    Start frontend dev server (for hot reload, proxy -> :3000)
  logs        Tail backend log
  logs-fe     Tail frontend dev server log
  ps          Show process status
  clean       stop + remove all build artifacts, caches, and dependencies
  capture     Start tcpdump capturing port 3000 traffic
  capture-stop    Stop tcpdump
  capture-analyze Analyze captured traffic

Commands (docker):
  start       docker compose -f ${COMPOSE_DOCKER} up -d
  stop        docker compose -f ${COMPOSE_DOCKER} down
  restart     stop + start
  build       Build Docker image
  debug       Attach shell into running container
  logs        Tail container logs
  clean       Stop and remove volumes (WARNING: deletes all data)
  ps          Show running containers
EOF
}

# ============================================================
# Local env — bare metal, no Docker
# ============================================================
cmd_local_start() {
  load_env
  mkdir -p "${LOG_DIR}"
  rm -rf ${LOG_DIR}/*.log
  ls ${LOG_DIR}/

  if [[ ! -f "${BINARY}" ]]; then
    cmd_local_build
  fi

  if pid_alive "${PID_BACKEND}"; then
    warn "Backend already running (PID: $(cat "${PID_BACKEND}"))"
  else
    log "Starting backend..."
    nohup "${BINARY}" >> "${LOG_BACKEND}" 2>&1 &
    echo $! > "${PID_BACKEND}"
    ok "Backend started (PID: $(cat "${PID_BACKEND}"))"
  fi

  ok "http://localhost:3000 | Status: http://localhost:3000/api/status"
}

cmd_local_stop() {
  log "Stopping local processes..."

  if pid_alive "${PID_BACKEND}"; then
    kill "$(cat "${PID_BACKEND}")" 2>/dev/null || true
    rm -f "${PID_BACKEND}"
    ok "Backend stopped"
  else
    warn "Backend not running"
  fi

  if pid_alive "${PID_FRONTEND}"; then
    kill "$(cat "${PID_FRONTEND}")" 2>/dev/null || true
    rm -f "${PID_FRONTEND}"
    ok "Frontend stopped"
  else
    warn "Frontend not running"
  fi

  ok "Done."
}

cmd_local_restart() { cmd_local_stop; sleep 1; cmd_local_start; }

cmd_local_build() {
  build_frontend "${FRONTEND_DIR}" "default"
  build_frontend "${CLASSIC_DIR}" "classic"

  log "Building Go binary..."
  go build -ldflags "-s -w" -o "${BINARY}" .
  ok "Built: ${BINARY}"
}

cmd_local_backend() {
  load_env
  mkdir -p "${LOG_DIR}"

  if pid_alive "${PID_BACKEND}"; then
    warn "Backend already running (PID: $(cat "${PID_BACKEND}"))"
    return
  fi

  if [[ ! -f "${BINARY}" ]]; then
    cmd_local_build
  fi

  log "Starting backend..."
  nohup "${BINARY}" >> "${LOG_BACKEND}" 2>&1 &
  echo $! > "${PID_BACKEND}"
  ok "Backend started (PID: $(cat "${PID_BACKEND}"))"
  ok "http://localhost:3000 | Status: http://localhost:3000/api/status"
}

cmd_local_frontend() {
  mkdir -p "${LOG_DIR}"

  if pid_alive "${PID_FRONTEND}"; then
    warn "Frontend already running (PID: $(cat "${PID_FRONTEND}"))"
    return
  fi

  if [[ ! -d "${FRONTEND_DIR}" ]]; then
    err "Frontend dir '${FRONTEND_DIR}' not found"
    exit 1
  fi

  log "Starting frontend dev server (port 3001, proxy -> :3000)..."
  (cd "${FRONTEND_DIR}" && nohup bun run dev >> "${LOG_FRONTEND}" 2>&1 & echo $! > "${PID_FRONTEND}")
  ok "Frontend started (PID: $(cat "${PID_FRONTEND}"))"
  ok "Frontend: http://localhost:3001"
}

cmd_local_logs() {
  if [[ -f "${LOG_BACKEND}" ]]; then
    tail -f "${LOG_BACKEND}"
  else
    warn "No backend log yet. Start backend first: ./run.sh local start"
  fi
}

cmd_local_logs_fe() {
  if [[ -f "${LOG_FRONTEND}" ]]; then
    tail -f "${LOG_FRONTEND}"
  else
    warn "No frontend log yet. Start frontend first: ./run.sh local frontend"
  fi
}

cmd_local_ps() {
  echo -e "${CYAN}--- Backend (port 3000) ---${NC}"
  if pid_alive "${PID_BACKEND}"; then
    ok "Running (PID: $(cat "${PID_BACKEND}"))"
  else
    warn "Not running"
  fi

  echo -e "${CYAN}--- Frontend dev (port 3001) ---${NC}"
  if pid_alive "${PID_FRONTEND}"; then
    ok "Running (PID: $(cat "${PID_FRONTEND}"))"
  else
    warn "Not running"
  fi
}

cmd_local_clean() {
  cmd_local_stop
  cmd_local_capture_stop 2>/dev/null || true

  log "Removing Go binary..."
  rm -f "${BINARY}"

  log "Cleaning Go build cache..."
  go clean -cache 2>/dev/null || true

  log "Removing frontend build outputs (dist)..."
  rm -rf "${FRONTEND_DIR}/dist"
  rm -rf "${CLASSIC_DIR}/dist"

  log "Removing frontend dependencies (node_modules)..."
  rm -rf "${FRONTEND_DIR}/node_modules"
  rm -rf "${CLASSIC_DIR}/node_modules"
  rm -rf ./web/node_modules

  log "Removing logs and PID files..."
  rm -rf "${LOG_DIR}"
  rm -f "${PID_BACKEND}" "${PID_FRONTEND}" "${PID_TCPDUMP}"

  pkill -f "rsbuild dev" 2>/dev/null || true

  ok "Cleaned — binary, build caches, node_modules, logs, and PID files removed."
  ok "Next: ./run.sh local build && ./run.sh local start"
}

# ============================================================
# Packet capture — tcpdump on port 3000
# ============================================================
cmd_local_capture() {
  mkdir -p "${CAPTURE_DIR}"

  if pid_alive "${PID_TCPDUMP}"; then
    warn "tcpdump already running (PID: $(cat "${PID_TCPDUMP}"))"
    return
  fi

  if ! command -v tcpdump &>/dev/null; then
    err "tcpdump not found. Install: sudo apt install tcpdump"
    exit 1
  fi

  log "Starting tcpdump on port 3000..."
  log "PCAP: ${CAPTURE_PCAP}"
  log "Text: ${CAPTURE_TXT}"

  sudo tcpdump -i any -A -s 0 port 3000 -w "${CAPTURE_PCAP}" > /dev/null 2>&1 &
  echo $! > "${PID_TCPDUMP}"

  sudo tcpdump -i any -A -s 0 port 3000 > "${CAPTURE_TXT}" 2>&1 &
  echo $(($(cat "${PID_TCPDUMP}") + 1)) >> /dev/null # rough tracking

  ok "Capture started (PID: $(cat "${PID_TCPDUMP}"))"
  ok "Run './run.sh local capture-analyze' to analyze results"
}

cmd_local_capture_stop() {
  log "Stopping tcpdump..."
  if pid_alive "${PID_TCPDUMP}"; then
    local pid=$(cat "${PID_TCPDUMP}")
    sudo kill "${pid}" 2>/dev/null || true
    rm -f "${PID_TCPDUMP}"
    ok "tcpdump stopped (PID: ${pid})"
  else
    warn "tcpdump not running"
  fi
  # Kill any lingering tcpdump on port 3000
  sudo pkill -f "tcpdump.*port 3000" 2>/dev/null || true
}

cmd_local_capture_analyze() {
  if [[ ! -f "${CAPTURE_TXT}" ]]; then
    err "No capture file found at ${CAPTURE_TXT}"
    err "Start capture first: ./run.sh local capture"
    exit 1
  fi

  local txt="${CAPTURE_TXT}"
  local report="${CAPTURE_REPORT}"

  log "Analyzing capture: ${txt}"

  {
    echo "=========================================="
    echo " Capture Report — $(date)"
    echo " File: ${txt}"
    echo "=========================================="
    echo ""

    # Count HTTP requests
    local req_count=$(grep -cE '^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) ' "${txt}" 2>/dev/null || echo 0)
    local resp_count=$(grep -cE '^HTTP/[0-9.]+ [0-9]{3}' "${txt}" 2>/dev/null || echo 0)

    echo "Summary: ${req_count} requests, ${resp_count} responses"
    echo ""

    # Extract request lines
    echo "=== Request Lines ==="
    grep -nE '^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) ' "${txt}" 2>/dev/null || echo "(none)"
    echo ""

    # Extract response status lines
    echo "=== Response Status Lines ==="
    grep -nE '^HTTP/[0-9.]+ [0-9]{3}' "${txt}" 2>/dev/null || echo "(none)"
    echo ""

    # Extract Host headers
    echo "=== Host Headers ==="
    grep -iE '^Host: ' "${txt}" 2>/dev/null || echo "(none)"
    echo ""

    # Extract Content-Type response headers
    echo "=== Content-Type (Response) ==="
    grep -iE '^Content-Type: ' "${txt}" 2>/dev/null || echo "(none)"
    echo ""

    # Count by status code
    echo "=== Status Code Distribution ==="
    grep -oE 'HTTP/[0-9.]+ [0-9]{3}' "${txt}" 2>/dev/null | awk '{print $2}' | sort | uniq -c | sort -rn || echo "(none)"
    echo ""

    # Extract JSON bodies (brief)
    echo "=== JSON Bodies (truncated) ==="
    grep -E '^\{' "${txt}" 2>/dev/null | head -30 || echo "(none)"
    echo ""

    echo "=========================================="
    echo " Full text: ${txt}"
    echo " PCAP file: ${CAPTURE_PCAP}"
    echo " Replay PCAP: tcpdump -r ${CAPTURE_PCAP} -A"
    echo "=========================================="
  } | tee "${report}"

  ok "Report saved: ${report}"
  echo ""
  log "Quick view: less ${txt}"
}

# ============================================================
# Docker env — containers
# ============================================================
cmd_docker_start() {
  log "Starting docker environment..."
  docker compose -f "${COMPOSE_DOCKER}" up -d
  ok "API: http://localhost:3000 | Status: http://localhost:3000/api/status"
}

cmd_docker_stop() {
  log "Stopping docker environment..."
  docker compose -f "${COMPOSE_DOCKER}" down
  ok "Stopped."
}

cmd_docker_restart() { cmd_docker_stop; cmd_docker_start; }

cmd_docker_build() {
  log "Building Docker image..."
  docker compose -f "${COMPOSE_DOCKER}" build
  ok "Built."
}

cmd_docker_debug() {
  local container
  container=$(docker compose -f "${COMPOSE_DOCKER}" ps -q new-api 2>/dev/null || true)
  if [[ -z "$container" ]]; then
    err "Container 'new-api' is not running. Start it first: ./run.sh docker start"
    exit 1
  fi
  log "Attaching to new-api container (Ctrl+D to exit)..."
  docker exec -it "$container" /bin/bash 2>/dev/null || docker exec -it "$container" /bin/sh
}

cmd_docker_logs() {
  docker compose -f "${COMPOSE_DOCKER}" logs -f
}

cmd_docker_clean() {
  log "Removing docker environment and volumes..."
  docker compose -f "${COMPOSE_DOCKER}" down -v
  ok "Cleaned."
}

cmd_docker_ps() {
  docker compose -f "${COMPOSE_DOCKER}" ps
}

# ============================================================
# Main
# ============================================================
main() {
  local env="${1:-}"
  local cmd="${2:-}"

  if [[ -z "$env" ]]; then
    usage; exit 1
  fi

  # Flat shortcuts (./run.sh build) — default to docker env
  if [[ -z "$cmd" ]]; then
    case "${env}" in
      build)   cmd_docker_build ;;
      start)   cmd_docker_start ;;
      stop)    cmd_docker_stop ;;
      restart) cmd_docker_restart ;;
      debug)   cmd_docker_debug ;;
      logs)    cmd_docker_logs ;;
      clean)   cmd_docker_clean ;;
      ps)      cmd_docker_ps ;;
      *)       usage; exit 1 ;;
    esac
    return
  fi

  # Two-level: ./run.sh <env> <cmd>
  case "${env}" in
    local)
      case "${cmd}" in
        start)     cmd_local_start ;;
        stop)      cmd_local_stop ;;
        restart)   cmd_local_restart ;;
        build)     cmd_local_build ;;
        backend)   cmd_local_backend ;;
        frontend)  cmd_local_frontend ;;
        logs)      cmd_local_logs ;;
        logs-fe)   cmd_local_logs_fe ;;
        ps)        cmd_local_ps ;;
        clean)           cmd_local_clean ;;
        capture)         cmd_local_capture ;;
        capture-stop)    cmd_local_capture_stop ;;
        capture-analyze) cmd_local_capture_analyze ;;
        *)         usage; exit 1 ;;
      esac
      ;;
    docker)
      case "${cmd}" in
        start)   cmd_docker_start ;;
        stop)    cmd_docker_stop ;;
        restart) cmd_docker_restart ;;
        build)   cmd_docker_build ;;
        debug)   cmd_docker_debug ;;
        logs)    cmd_docker_logs ;;
        clean)   cmd_docker_clean ;;
        ps)      cmd_docker_ps ;;
        *)       usage; exit 1 ;;
      esac
      ;;
    *)
      usage; exit 1
      ;;
  esac
}

main "${1:-}" "${2:-}"
