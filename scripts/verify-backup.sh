#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
snapshot_dir=${1:?Pass the snapshot directory created by make backup}
snapshot_dir=$(realpath -- "$snapshot_dir")
test -f "$snapshot_dir/database.dump"
test -f "$snapshot_dir/objects.json"
verify_checksum() {
  local checksum_file=$1 verified_file=$2 expected_digest ignored_path actual_digest
  read -r expected_digest ignored_path < "$checksum_file"
  [[ "$expected_digest" =~ ^[a-f0-9]{64}$ ]]
  actual_digest=$(sha256sum "$verified_file")
  [[ "${actual_digest%% *}" == "$expected_digest" ]]
}
verify_checksum "$snapshot_dir/database.sha256" "$snapshot_dir/database.dump"
verify_checksum "$snapshot_dir/objects.sha256" "$snapshot_dir/objects.json"
go -C apps/api run ./cmd/asset-backup -directory "$snapshot_dir" -verify
restore_database="myjira_restore_test_$(date -u +%Y%m%d%H%M%S)_$$"
restore_bucket="myjira-restore-test-$(date -u +%Y%m%d%H%M%S)-$$-$RANDOM"
[[ "$restore_database" =~ ^myjira_restore_test_[0-9]+_[0-9]+$ ]]
[[ "$restore_bucket" =~ ^myjira-restore-test-[0-9]+-[0-9]+-[0-9]+$ ]]
restore_database_url=$(RESTORE_DATABASE="$restore_database" node -e 'try { const u = new URL(process.env.DATABASE_URL); if (!["postgres:","postgresql:"].includes(u.protocol)) throw new Error(); u.pathname = "/" + process.env.RESTORE_DATABASE; process.stdout.write(u.toString()); } catch { console.error("A valid PostgreSQL DATABASE_URL is required"); process.exit(1); }')
docker compose exec -T postgres createdb -U myjira "$restore_database"
# The trap is installed only after this exact test database was created.
cleanup_database() {
  local restore_status=$?
  trap - EXIT
  if docker compose exec -T postgres dropdb -U myjira "$restore_database"; then
    printf 'Removed the newly created verification database %s.\n' "$restore_database"
  else
    restore_status=1
  fi
  exit "$restore_status"
}
trap cleanup_database EXIT
docker compose exec -T postgres pg_restore -U myjira --dbname="$restore_database" --exit-on-error < "$snapshot_dir/database.dump"
DATABASE_URL="$restore_database_url" go -C apps/api run ./cmd/migrate
docker compose exec -T postgres psql -U myjira -d "$restore_database" -v ON_ERROR_STOP=1 -c 'SELECT (SELECT count(*) FROM users) AS users,(SELECT count(*) FROM projects) AS projects,(SELECT count(*) FROM work_items) AS work_items,(SELECT count(*) FROM pages) AS pages,(SELECT count(*) FROM file_assets) AS assets;'
DATABASE_URL="$restore_database_url" go -C apps/api run ./cmd/asset-backup -directory "$snapshot_dir" -restore -bucket "$restore_bucket" -verify-application -cleanup
printf 'Verified database, restored object checksums/metadata, and authorized application attachment reads for %s.\n' "$restore_database"
