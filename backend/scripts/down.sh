#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
for f in .bin/authority.pid .bin/student.pid .bin/reference.pid .bin/buggy.pid; do
  if [[ -f "$f" ]] && kill -0 "$(cat "$f")" 2>/dev/null; then
    kill "$(cat "$f")" 2>/dev/null || true
  fi
done
COMPOSE="${COMPOSE:-docker compose}"
if ! docker info >/dev/null 2>&1; then
  COMPOSE="sudo docker compose"
fi
$COMPOSE down >/dev/null 2>&1 || true
echo "stopped"
