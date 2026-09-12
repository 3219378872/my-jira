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

.PHONY: requirements-infra requirements-integration
requirements-infra:
	docker compose -f compose.requirements-test.yaml up -d --wait
	docker compose -f compose.requirements-test.yaml exec -T postgres psql -U myjira_test -d myjira_requirements_test -v ON_ERROR_STOP=1 -c 'CREATE SCHEMA IF NOT EXISTS requirements_acceptance_v1'

requirements-integration:
	cd apps/api && TEST_DATABASE_URL='postgres://myjira_test:requirements_local_test@127.0.0.1:35432/myjira_requirements_test?sslmode=disable' TEST_S3_ENDPOINT=127.0.0.1:39000 TEST_S3_ACCESS_KEY=myjira_requirements_test TEST_S3_SECRET_KEY=requirements_storage_test S3_TEST_ENDPOINT=127.0.0.1:39000 S3_ACCESS_KEY=myjira_requirements_test S3_SECRET_KEY=requirements_storage_test S3_USE_SSL=false go test ./... -count=1

.PHONY: requirements-migrate requirements-seed
requirements-migrate:
	bash scripts/requirements-test-runtime.sh migrate

requirements-seed:
	bash scripts/requirements-test-runtime.sh seed
