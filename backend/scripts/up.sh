#!/usr/bin/env bash
# Start Postgres (Docker) plus the Go services this kit is allowed to run.
# Organiser tree: student + reference + buggy (reference/buggy live under internal/targets).
# Packed Backend/Mobile kits: student (+ authority) only.
# Packed Platform/SDET kits: prebuilt binaries in .bin/ or targets/.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
mkdir -p .bin data

KIT_TRACK="${KIT_TRACK:-}"
if [[ -f "$ROOT/KIT_TRACK" ]]; then
  KIT_TRACK="$(cat "$ROOT/KIT_TRACK")"
fi
if [[ "$KIT_TRACK" == "ai" ]]; then
  echo "AI / Ask the Drop kit is offline. Nothing to start."
  echo "Run: make check TRACK=ai"
  exit 0
fi

COMPOSE="${COMPOSE:-docker compose}"
if ! docker info >/dev/null 2>&1; then
  COMPOSE="sudo docker compose"
fi
$COMPOSE up -d postgres
$COMPOSE stop reservation-api reference-api buggy-api >/dev/null 2>&1 || true

echo "waiting for Postgres on :5432..."
for i in $(seq 1 40); do
  if (echo >/dev/tcp/127.0.0.1/5432) >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

TARGETS="${LAUNCHDAY_TARGETS:-$ROOT/../internal/targets}"

build() { go build -C "$1" -o "$ROOT/.bin/$2" .; }

build "$ROOT/authority" authority
if [[ -d "$ROOT/backend/go" ]]; then
  build "$ROOT/backend/go" student
fi
# Organiser-only sources. Packed kits get binaries from pack-kits.sh instead.
if [[ -d "$TARGETS/reference" && ! -x "$ROOT/.bin/reference" ]]; then
  build "$TARGETS/reference" reference
fi
if [[ -d "$TARGETS/buggy" && ! -x "$ROOT/.bin/buggy" ]]; then
  build "$TARGETS/buggy" buggy
fi

stop_pid() {
  local f="$1"
  if [[ -f "$f" ]] && kill -0 "$(cat "$f")" 2>/dev/null; then
    kill "$(cat "$f")" 2>/dev/null || true
  fi
}
stop_pid .bin/authority.pid
stop_pid .bin/student.pid
stop_pid .bin/reference.pid
stop_pid .bin/buggy.pid

if ! curl -sf http://127.0.0.1:9000/health >/dev/null 2>&1; then
  PORT=9000 .bin/authority >data/authority.log 2>&1 & echo $! > .bin/authority.pid
  sleep 0.3
fi

AUTHORITY_URL="${AUTHORITY_URL:-http://127.0.0.1:9000}"

start_api() {
  local name="$1" port="$2" db="$3"
  if [[ ! -x "$ROOT/.bin/$name" ]]; then
    return 0
  fi
  DATABASE_URL="postgres://launchday:launchday@127.0.0.1:5432/${db}?sslmode=disable" \
    AUTHORITY_URL="$AUTHORITY_URL" PORT="$port" \
    "$ROOT/.bin/$name" >"data/${name}.log" 2>&1 & echo $! > ".bin/${name}.pid"
}

# Packed backend/mobile: student only. Organiser, platform, and SDET: extra binaries if present.
start_api student 8080 student
if [[ "$KIT_TRACK" != "backend" && "$KIT_TRACK" != "mobile" ]]; then
  start_api reference 8081 reference
  start_api buggy 8082 buggy
fi

echo "waiting for APIs..."
for i in $(seq 1 30); do
  ok=1
  curl -sf http://127.0.0.1:9000/health >/dev/null || ok=0
  if [[ -f .bin/student.pid ]]; then
    curl -sf http://127.0.0.1:8080/health >/dev/null || ok=0
  fi
  if [[ -f .bin/reference.pid ]]; then
    curl -sf http://127.0.0.1:8081/health >/dev/null || ok=0
  fi
  if [[ -f .bin/buggy.pid ]]; then
    curl -sf http://127.0.0.1:8082/health >/dev/null || ok=0
  fi
  [[ $ok -eq 1 ]] && break
  sleep 1
done

echo "authority     http://127.0.0.1:9000"
[[ -f .bin/student.pid ]] && echo "student API   http://127.0.0.1:8080"
[[ -f .bin/reference.pid ]] && echo "reference API http://127.0.0.1:8081"
[[ -f .bin/buggy.pid ]] && echo "buggy API     http://127.0.0.1:8082"
echo "optional: $COMPOSE up -d otel-collector prometheus grafana"
