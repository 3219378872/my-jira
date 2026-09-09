#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
mkdir -p .local/backups
snapshot_dir=$(mktemp -d "$PWD/.local/backups/snapshot.XXXXXXXX")
docker compose exec -T postgres pg_dump -U myjira -d myjira --format=custom > "$snapshot_dir/database.dump"
go -C apps/api run ./cmd/asset-backup -directory "$snapshot_dir"
(cd "$snapshot_dir" && sha256sum database.dump > database.sha256 && sha256sum objects.json > objects.sha256)
printf 'Snapshot created: %s\n' "$snapshot_dir"
printf 'Keep deployment APP_ENCRYPTION_KEY and any explicit LIVE_SERVICE_KEY separately with your secure backups.\n'
