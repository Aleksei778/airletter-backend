include .env

SHELL = /bin/sh
UID := $(shell id -u)
COMPOSE = docker compose -p airletter -f docker-compose.local.yaml
NETWORK = airletter_network

.PHONY: up down restart stop \
        go db db-c redis redis-c \
        migrate-create migrate-up migrate-down migrate-version migrate-force

unquote = $(subst ",,$(subst ',,$(1)))

MIGRATE_IMAGE = migrate/migrate:v4.20.1
DB_URL = postgres://$(call unquote,$(DB_USER)):$(call unquote,$(DB_PASS))@$(call unquote,$(DB_HOST)):$(call unquote,$(DB_PORT))/$(call unquote,$(DB_NAME))?sslmode=disable
MIGRATE = docker run --rm --network $(NETWORK) -v $(CURDIR)/migrations:/migrations $(MIGRATE_IMAGE) -path /migrations -database "$(DB_URL)"

# === DOCKER OPERATIONS ===
network:
	@docker network inspect ${NETWORK} >/dev/null 2>&1 || docker network create --driver bridge ${NETWORK}

up:
	@env UID=${UID} $(COMPOSE) up -d --remove-orphans

down:
	@env UID=${UID} $(COMPOSE) down -v

restart: down up

stop:
	@env UID=${UID} $(COMPOSE) stop

# === CONTAINER ACCESS ===
go:
	@env UID=${UID} $(COMPOSE) exec app sh

db:
	@env UID=${UID} $(COMPOSE) exec db bash

# usage: make dbc username=YOUR_USERNAME
db-c:
	@env UID=${UID} $(COMPOSE) exec db psql -U $(user) -d db

redis:
	@env UID=${UID} $(COMPOSE) exec redis bash

redis-c:
	@env UID=${UID} $(COMPOSE) exec redis redis-cli

# === MIGRATIONS (migrations/*.sql, applied by the API on start) ===
# usage: make migrate-create name=add_campaign_notes
migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=add_something" && exit 1)
	@docker run --rm -u $(UID) -v $(CURDIR)/migrations:/migrations $(MIGRATE_IMAGE) create -ext sql -dir /migrations -seq $(name)

migrate-up:
	@$(MIGRATE) up

# usage: make migrate-down [steps=1]
migrate-down:
	@$(MIGRATE) down $(or $(steps),1)

migrate-version:
	@$(MIGRATE) version

# after a failed migration the schema is marked dirty: fix it by hand, then
# usage: make migrate-force version=N
migrate-force:
	@test -n "$(version)" || (echo "usage: make migrate-force version=N" && exit 1)
	@$(MIGRATE) force $(version)

# === HELP ===
help:
	@echo "Makefile Commands:"
	@echo ""
	@echo "  🚀 Docker Operations:"
	@echo "    up          - Start Docker containers"
	@echo "    down        - Stop and remove Docker containers"
	@echo "    restart     - Restart Docker containers"
	@echo "    stop        - Stop Docker containers"
	@echo ""
	@echo "  🐳 Container Access:"
	@echo "    go         - Access Go container bash"
	@echo "    db         - Access database container bash"
	@echo "    db-c       - Access PostgreSQL console"
	@echo "    redis      - Access Redis container bash"
	@echo "    redis-c    - Access Redis CLI"
	@echo ""
	@echo "  🗄  Migrations (golang-migrate, migrations/*.sql):"
	@echo "    migrate-create name=x - Create a new up/down migration pair"
	@echo "    migrate-up            - Apply pending migrations (the API also does it on start)"
	@echo "    migrate-down [steps=1]- Roll back migrations"
	@echo "    migrate-version       - Show the current schema version"
	@echo "    migrate-force version=N - Clear the dirty flag after a failed migration"
