#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
test_project=my-jira-bootstrap-test
for resource in container volume network; do
  if [[ "$resource" == container ]]; then
    existing=$(docker ps -aq --filter "label=com.docker.compose.project=$test_project")
  else
    existing=$(docker "$resource" ls -q --filter "label=com.docker.compose.project=$test_project")
  fi
  if [[ -n "$existing" ]]; then
    echo "The dedicated installation test $test_project already owns a $resource; inspect it before rerunning." >&2
    exit 1
  fi
done

export APP_PORT=4181 POSTGRES_PORT=25433 REDIS_PORT=26380
export S3_PORT=29002 S3_CONSOLE_PORT=29003 SMTP_LOCAL_PORT=21026 MAILPIT_PORT=28026
export DEPLOY_ORIGIN=http://127.0.0.1:4181
export E2E_FIRST_RUN=1 E2E_BASE_URL="$DEPLOY_ORIGIN" E2E_MAILPIT_URL=http://127.0.0.1:28026
compose=(docker compose -p "$test_project" -f compose.yaml -f compose.app.yaml)
"${compose[@]}" build --build-arg "GOPROXY=${MYJIRA_BUILD_GOPROXY:-https://proxy.golang.org}"
cleanup() {
  echo "Removing only the temporary $test_project stack and its acceptance data."
  "${compose[@]}" down --volumes
}
trap cleanup EXIT
"${compose[@]}" up -d --no-build --wait --wait-timeout 90
pnpm exec playwright test tests/e2e/first-run.spec.ts --output=.local/evidence/bootstrap-playwright --reporter=list
pnpm exec playwright test tests/e2e/queue-restart.spec.ts --output=.local/evidence/queue-restart-playwright --reporter=list
