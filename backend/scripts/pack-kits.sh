#!/usr/bin/env bash
# Build per-track tarballs. Candidates must receive these, not the organiser tree.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HACK="$(cd "$ROOT/.." && pwd)"
OUT="${HACK}/dist/kits"
TARGETS="${HACK}/internal/targets"
rm -rf "$OUT"
mkdir -p "$OUT"

build_bin() {
  local src="$1" dest="$2"
  go build -C "$src" -o "$dest" .
}

echo "building target binaries (no source in Platform/SDET packs)..."
mkdir -p "$OUT/_bin"
build_bin "$TARGETS/reference" "$OUT/_bin/reference"
build_bin "$TARGETS/buggy" "$OUT/_bin/buggy"
build_bin "$ROOT/authority" "$OUT/_bin/authority"
build_bin "$ROOT/backend/go" "$OUT/_bin/student"

pack() {
  local track="$1"
  local dest="$OUT/$track"
  mkdir -p "$dest"
  echo "$track" > "$dest/KIT_TRACK"
  cp "$ROOT/Makefile" "$dest/"
  cp "$ROOT/.env.example" "$dest/"
  cp "$ROOT/.gitignore" "$dest/"
  cp "$ROOT/SUBMISSION.md" "$dest/"
  cp -a "$ROOT/scripts" "$dest/"
  cp -a "$ROOT/authority" "$dest/"
  cp -a "$ROOT/seed" "$dest/"
  cp -a "$ROOT/checks" "$dest/"
}

# --- backend: student APIs only ---
pack backend
mkdir -p "$OUT/backend/backend"
cp -a "$ROOT/backend/go" "$OUT/backend/backend/go"
cp -a "$ROOT/backend/java" "$OUT/backend/backend/java"
cp -a "$ROOT/backend/kotlin" "$OUT/backend/backend/kotlin"
cat > "$OUT/backend/docker-compose.yml" <<'YAML'
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: postgres
    ports:
      - "5432:5432"
    volumes:
      - ./seed/init.sql:/docker-entrypoint-initdb.d/init.sql:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 2s
      timeout: 3s
      retries: 20
YAML
cat > "$OUT/backend/README.md" <<'EOF'
# Launch Day — Backend kit

This zip is **only** the Backend track. There is no reference implementation here.

```
make up
make check TRACK=backend
```

Start from `backend/go` (or java / kotlin). Do not modify `authority/` or `checks/`.
AI chatbots such as ChatGPT may be used only for assistance. Automated AI agents, autonomous systems, IDE-integrated AI chatbots/agents, and tools that generate substantial or complete solution code are restricted. You must be able to explain and modify your work live. A sealed constraint lands at 0:15.
EOF

# --- mobile ---
pack mobile
mkdir -p "$OUT/mobile/backend"
cp -a "$ROOT/mobile" "$OUT/mobile/mobile"
cp -a "$ROOT/backend/go" "$OUT/mobile/backend/go"
cp "$OUT/backend/docker-compose.yml" "$OUT/mobile/docker-compose.yml" 2>/dev/null || true
cat > "$OUT/mobile/README.md" <<'EOF'
# Launch Day — Mobile kit

Item list works. Item Detail / Reserve is unfinished.

```
make up
# Reservation API on :8080  ·  Authority on :9000
```

AI chatbots such as ChatGPT may be used only for assistance. Automated AI agents, autonomous systems, IDE-integrated AI chatbots/agents, and tools that generate substantial or complete solution code are restricted. You must be able to explain and modify your work live. A sealed constraint lands at 0:15.
EOF

# --- platform: reference binary only ---
pack platform
cp -a "$ROOT/platform" "$OUT/platform/platform"
mkdir -p "$OUT/platform/.bin"
cp "$OUT/_bin/reference" "$OUT/platform/.bin/reference"
cp "$OUT/_bin/authority" "$OUT/platform/.bin/authority"
# slim compose: no source builds for reference
python3 - <<'PY'
from pathlib import Path
src = Path("/workspace/hackathon/starter-kit/docker-compose.yml").read_text()
# keep postgres, otel, prometheus, grafana, authority; drop java/kotlin/student/buggy/reference builds
keep_prefixes = ("services:", "  postgres:", "  authority:", "  otel-collector:", "  prometheus:", "  grafana:")
# simpler: copy full compose but rewrite reference-api build to use binary image folder
text = src.replace("build: ../internal/targets/reference", "build: ./targets/reference")
# remove buggy-api and reservation-api and java/kotlin blocks by writing a minimal file
Path("/workspace/hackathon/dist/kits/platform/docker-compose.yml").write_text("""
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: postgres
    ports:
      - "5432:5432"
    volumes:
      - ./seed/init.sql:/docker-entrypoint-initdb.d/init.sql:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 2s
      timeout: 3s
      retries: 20

  otel-collector:
    image: otel/opentelemetry-collector-contrib:0.109.0
    command: ["--config=/etc/otel/config.yaml"]
    volumes:
      - ./platform/otel/otel-collector-config.yaml:/etc/otel/config.yaml:ro
    ports:
      - "4317:4317"
      - "4318:4318"
      - "8889:8889"

  prometheus:
    image: prom/prometheus:v2.54.1
    volumes:
      - ./platform/prometheus/prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - ./platform/prometheus/rules:/etc/prometheus/rules:ro
    ports:
      - "9090:9090"
    depends_on:
      - otel-collector

  grafana:
    image: grafana/grafana:11.2.0
    environment:
      GF_SECURITY_ADMIN_USER: admin
      GF_SECURITY_ADMIN_PASSWORD: admin
      GF_AUTH_ANONYMOUS_ENABLED: "true"
      GF_AUTH_ANONYMOUS_ORG_ROLE: Admin
    volumes:
      - ./platform/grafana/provisioning:/etc/grafana/provisioning:ro
      - ./platform/grafana/dashboards:/var/lib/grafana/dashboards:ro
    ports:
      - "3000:3000"
    depends_on:
      - prometheus
""")
PY
cat > "$OUT/platform/README.md" <<'EOF'
# Launch Day — Platform kit

The Reservation API is a **binary** at `.bin/reference` (no source). Collector pipeline is broken.

```
make up
make check TRACK=platform
```

`make up` starts Postgres and `.bin/reference` on :8081. Then:
`docker compose up -d otel-collector prometheus grafana`

AI chatbots such as ChatGPT may be used only for assistance. Automated AI agents, autonomous systems, IDE-integrated AI chatbots/agents, and tools that generate substantial or complete solution code are restricted. You must be able to explain and modify your work live. A sealed constraint lands at 0:15.
EOF

# --- sdet: both binaries, no source ---
pack sdet
cp -a "$ROOT/tests" "$OUT/sdet/tests"
mkdir -p "$OUT/sdet/.bin"
cp "$OUT/_bin/reference" "$OUT/sdet/.bin/reference"
cp "$OUT/_bin/buggy" "$OUT/sdet/.bin/buggy"
cp "$OUT/_bin/authority" "$OUT/sdet/.bin/authority"
cat > "$OUT/sdet/README.md" <<'EOF'
# Launch Day — Test Engineering kit

`reference` (:8081) and `buggy` (:8082) are **binaries**. There is no API source in this zip.

```
make up
make check TRACK=sdet SDET_LANG=go
```

Implement I1–I6 in `tests/go` (or java / kotlin). Do not modify `checks/` or `authority/`.

AI chatbots such as ChatGPT may be used only for assistance. Automated AI agents, autonomous systems, IDE-integrated AI chatbots/agents, and tools that generate substantial or complete solution code are restricted. You must be able to explain and modify your work live. A sealed constraint lands at 0:15.
EOF

# --- ai: offline log + questions, no APIs ---
pack ai
cp -a "$ROOT/ai" "$OUT/ai/ai"
chmod +x "$OUT/ai/ai/run.sh" "$OUT/ai/ai/ask.py"
rm -f "$OUT/ai/ai/out/"*.jsonl
# AI candidates do not need authority source or seed SQL
rm -rf "$OUT/ai/authority" "$OUT/ai/seed"
cat > "$OUT/ai/docker-compose.yml" <<'YAML'
# Ask the Drop is offline. `make up` does nothing. `make check TRACK=ai` is the gate.
services: {}
YAML
cat > "$OUT/ai/README.md" <<'EOF'
# Launch Day — AI / Data Science kit

This zip is **only** the Ask the Drop track. There is no Reservation API and no live model.

```
make check TRACK=ai
```

Start from `ai/ask.py`. Write `ai/out/answers.jsonl` and `ai/out/scores.jsonl`.
Do not modify `checks/` or `ai/data/events.jsonl`.

`make check` is offline. Do not call a hosted model from `run.sh`.

AI chatbots such as ChatGPT may be used only for assistance. Automated AI agents, autonomous systems, IDE-integrated AI chatbots/agents, and tools that generate substantial or complete solution code are restricted. You must be able to explain and modify your work live. A sealed constraint lands at 0:15.
EOF

# tarballs
rm -rf "$OUT/_bin"
for t in backend mobile platform sdet ai; do
  tar -C "$OUT" -czf "$OUT/${t}.tar.gz" "$t"
  echo "wrote $OUT/${t}.tar.gz"
done
echo "packs ready under $OUT"
