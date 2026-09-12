#!/usr/bin/env bash
# This fixed configuration only points to compose.requirements-test.yaml.
set -euo pipefail
test_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
export DATABASE_URL='postgres://myjira_test:requirements_local_test@127.0.0.1:35432/myjira_requirements_test?sslmode=disable&search_path=requirements_acceptance_v1'
export TEST_DATABASE_URL="$DATABASE_URL"
export REDIS_ADDR=127.0.0.1:36379
export API_ADDR=127.0.0.1:18088
export APP_ORIGIN=http://127.0.0.1:14173
export ALLOWED_ORIGINS=http://127.0.0.1:14173,http://127.0.0.1:18088
export API_PUBLIC_URL=http://127.0.0.1:14173
export APP_URL=http://127.0.0.1:14173
export COOKIE_SECURE=false
# Public test-only encryption material; never use for a deployed instance.
export APP_ENCRYPTION_KEY=bXlqaXJhLXJlcXVpcmVtZW50cy10ZXN0LWtleS0wMDE=
export LIVE_SERVICE_KEY=myjira-requirements-test-live-only
export S3_ENDPOINT=127.0.0.1:39000
export S3_BUCKET=myjira-requirements-test
export S3_ACCESS_KEY=myjira_requirements_test
export S3_SECRET_KEY=requirements_storage_test
export S3_USE_SSL=false
export SMTP_HOST=127.0.0.1
export SMTP_PORT=31025
export SMTP_FROM=noreply@requirements.test
export LIVE_PORT=13101
export LIVE_API_URL=http://127.0.0.1:18088
export VITE_API_PROXY_URL=http://127.0.0.1:18088
export VITE_LIVE_PROXY_URL=http://127.0.0.1:13101
export MYJIRA_API_URL=http://127.0.0.1:18088
export E2E_BASE_URL=http://127.0.0.1:14173
cd "$test_root"
case "${1:-}" in
  api|worker|migrate)
    mode="$1"
    cd apps/api
    exec go run "./cmd/$mode"
    ;;
  web) exec pnpm --filter @my-jira/web exec vite --port 14173 ;;
  live) exec pnpm --filter @myjira/live dev ;;
  seed) exec node scripts/seed-requirements.mjs ;;
  *) printf '%s\n' 'Usage: bash scripts/requirements-test-runtime.sh api|worker|migrate|web|live|seed' >&2; exit 2 ;;
esac
