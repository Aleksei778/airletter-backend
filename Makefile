include .env

SHELL = /bin/sh
UID := $(shell id -u)
COMPOSE = docker compose -p airletter -f docker-compose.local.yaml
NETWORK = airletter_network

.PHONY: up down restart stop \
        go db db-c redis redis-c

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