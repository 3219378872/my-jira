.PHONY: infra infra-stop api web live check test build

ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: migrate worker seed

migrate:
	cd apps/api && go run ./cmd/migrate

worker:
	cd apps/api && go run ./cmd/worker

seed:
	node scripts/seed-demo.mjs

infra:
	docker compose up -d postgres redis minio mailpit

infra-stop:
	docker compose stop

api:
	cd apps/api && go run ./cmd/api

web:
	pnpm --filter @my-jira/web dev

live:
	pnpm --filter @myjira/live dev

check:
	pnpm typecheck
	pnpm -r --if-present check
	cd apps/api && go vet ./...

test:
	cd apps/api && go test ./...
	pnpm -r --if-present test

build:
	pnpm -r --if-present build
	cd apps/api && go build ./cmd/...

.PHONY: app app-build app-stop e2e integration
app:
	docker compose -f compose.yaml -f compose.app.yaml up -d --build

app-build:
	docker compose -f compose.yaml -f compose.app.yaml build

app-stop:
	docker compose -f compose.yaml -f compose.app.yaml stop web live api worker

e2e:
	pnpm test:e2e

integration:
	@test -n "$$TEST_DATABASE_URL" || (echo 'Set TEST_DATABASE_URL to the isolated myjira_test database'; exit 1)
	cd apps/api && go test ./... -count=1

.PHONY: backup verify-backup
backup:
	bash scripts/backup.sh

verify-backup:
	bash scripts/verify-backup.sh "$(SNAPSHOT)"

.PHONY: verify-installation
verify-installation:
	bash scripts/verify-installation.sh
