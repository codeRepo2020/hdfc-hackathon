# Launch Day starter kit
#   make up          start the stack
#   make check       run checks (TRACK=backend|platform|sdet|mobile|ai|smoke)
#   make authority MODE=down

# Prefer docker without sudo; fall back if the daemon requires it.
ifeq ($(shell docker info >/dev/null 2>&1 && echo ok),ok)
COMPOSE ?= docker compose
else
COMPOSE ?= sudo docker compose
endif
TRACK   ?= smoke
MODE    ?= healthy
BACKEND ?= go
SDET_LANG ?= go
MOBILE_PLATFORM ?= flutter
KIT_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

.PHONY: up down logs ps build check authority demo tidy smoke reset

up:
	@chmod +x scripts/up.sh scripts/down.sh
	COMPOSE="$(COMPOSE)" ./scripts/up.sh

down:
	@chmod +x scripts/down.sh
	COMPOSE="$(COMPOSE)" ./scripts/down.sh

logs:
	$(COMPOSE) logs -f --tail=100

ps:
	$(COMPOSE) ps

build:
	$(COMPOSE) build

authority:
	curl -sS -X POST http://127.0.0.1:9000/admin/mode \
		-H 'Content-Type: application/json' \
		-d '{"mode":"$(MODE)"}'
	@echo

reset:
	curl -sS -X POST http://127.0.0.1:9000/admin/reset -H 'Content-Type: application/json' -d '{}'
	curl -sS -X POST http://127.0.0.1:8080/admin/reset -H 'Content-Type: application/json' -d '{}'
	curl -sS -X POST http://127.0.0.1:8081/admin/reset -H 'Content-Type: application/json' -d '{}'
	curl -sS -X POST http://127.0.0.1:8082/admin/reset -H 'Content-Type: application/json' -d '{}'
	@echo

check:
	cd $(KIT_ROOT)/checks && \
	TRACK=$(TRACK) \
	API_URL=$${API_URL:-http://127.0.0.1:8080} \
	AUTHORITY_URL=$${AUTHORITY_URL:-http://127.0.0.1:9000} \
	REFERENCE_URL=$${REFERENCE_URL:-http://127.0.0.1:8081} \
	BUGGY_URL=$${BUGGY_URL:-http://127.0.0.1:8082} \
	KIT_ROOT=$(KIT_ROOT) \
	SDET_LANG=$(SDET_LANG) \
	MOBILE_PLATFORM=$(MOBILE_PLATFORM) \
	COMPOSE="$(COMPOSE)" \
	KILL_SERVICE=$${KILL_SERVICE:-reservation-api} \
	go run .

demo:
	API_URL=$${API_URL:-http://127.0.0.1:8081} bash platform/chaos/outage-demo.sh || \
		(echo "write platform/chaos/outage-demo.sh (see Platform brief)"; exit 1)

tidy:
	cd authority && go mod tidy
	cd backend/go && go mod tidy
	cd $(KIT_ROOT)/../internal/targets/reference && go mod tidy
	cd $(KIT_ROOT)/../internal/targets/buggy && go mod tidy
	cd checks && go mod tidy
	cd tests/go && go mod tidy

smoke:
	$(MAKE) check TRACK=smoke
