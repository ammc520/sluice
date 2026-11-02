#!/usr/bin/env bash
# Daily encrypted PostgreSQL backup for the Compose deployment.
#
#   BACKUP_PASSPHRASE_FILE=/etc/api-gateway/backup.passphrase \
#   RCLONE_REMOTE=cos:company-backups/api-gateway \
#   bash deploy/backup.sh
#
# The passphrase must differ from MASTER_KEY. The dump is encrypted before it
# touches disk, checksummed, and retained locally for BACKUP_RETENTION_DAYS.
set -Eeuo pipefail
umask 077

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd -- "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="${ENV_FILE:-$REPO_DIR/.env.production}"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_DIR/docker-compose.yml}"
BACKUP_DIR="${BACKUP_DIR:-$REPO_DIR/backups}"
RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-7}"

log() {
	printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2
}

die() {
	log "ERROR: $*"
	exit 1
}

read_env() {
	awk -v key="$1" '
		{
			pos = index($0, "=")
			if (pos == 0 || substr($0, 1, pos - 1) != key) next
			val = substr($0, pos + 1)
			gsub(/\r$/, "", val)
			first = substr(val, 1, 1)
			last = substr(val, length(val), 1)
			if (length(val) >= 2 &&
			    ((first == "\"" && last == "\"") ||
			     (first == "\047" && last == "\047"))) {
				val = substr(val, 2, length(val) - 2)
			}
			print val
			exit
		}
	' "$ENV_FILE"
}

cleanup() {
	if [[ -n "${tmp_file:-}" && -f "$tmp_file" ]]; then
		rm -f -- "$tmp_file"
	fi
}
trap cleanup EXIT

[[ -f "$ENV_FILE" ]] || die "env file not found: $ENV_FILE"
[[ -f "$COMPOSE_FILE" ]] || die "compose file not found: $COMPOSE_FILE"
[[ "$RETENTION_DAYS" =~ ^[0-9]+$ ]] || die "BACKUP_RETENTION_DAYS must be an integer"
command -v docker >/dev/null 2>&1 || die "docker is required"
command -v openssl >/dev/null 2>&1 || die "openssl is required"

POSTGRES_USER="${POSTGRES_USER:-$(read_env POSTGRES_USER)}"
POSTGRES_DB="${POSTGRES_DB:-$(read_env POSTGRES_DB)}"
[[ -n "$POSTGRES_USER" ]] || die "POSTGRES_USER is not set"
[[ -n "$POSTGRES_DB" ]] || die "POSTGRES_DB is not set"

passphrase="${BACKUP_PASSPHRASE:-}"
if [[ -z "$passphrase" && -n "${BACKUP_PASSPHRASE_FILE:-}" ]]; then
	[[ -r "$BACKUP_PASSPHRASE_FILE" ]] || die "cannot read BACKUP_PASSPHRASE_FILE"
	passphrase="$(tr -d '\r\n' <"$BACKUP_PASSPHRASE_FILE")"
fi
[[ -n "$passphrase" ]] || die "set BACKUP_PASSPHRASE or BACKUP_PASSPHRASE_FILE"
(( ${#passphrase} >= 16 )) || die "backup passphrase must be at least 16 characters"

master_key="${MASTER_KEY:-$(read_env MASTER_KEY)}"
if [[ -n "$master_key" && "$passphrase" = "$master_key" ]]; then
	die "BACKUP_PASSPHRASE must not be the same value as MASTER_KEY"
fi

mkdir -p -- "$BACKUP_DIR"
chmod 700 -- "$BACKUP_DIR"

if command -v flock >/dev/null 2>&1; then
	exec 9>"$BACKUP_DIR/.backup.lock"
	flock -n 9 || die "another backup is already running"
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
out_file="$BACKUP_DIR/gateway-$timestamp.dump.enc"
tmp_file="$BACKUP_DIR/.gateway-$timestamp.dump.enc.tmp"

log "dumping database $POSTGRES_DB"
docker compose \
	--env-file "$ENV_FILE" \
	-f "$COMPOSE_FILE" \
	exec -T postgres \
	pg_dump --format=custom --no-owner --no-privileges \
	--username "$POSTGRES_USER" --dbname "$POSTGRES_DB" |
	BACKUP_PASSPHRASE="$passphrase" openssl enc \
		-aes-256-cbc -salt -pbkdf2 -iter 200000 \
		-pass env:BACKUP_PASSPHRASE \
		-out "$tmp_file"

[[ -s "$tmp_file" ]] || die "backup file is empty"
mv -f -- "$tmp_file" "$out_file"
tmp_file=""
chmod 600 -- "$out_file"

sha256sum "$out_file" >"$out_file.sha256"
chmod 600 -- "$out_file.sha256"

log "backup written: $out_file"

if [[ -n "${RCLONE_REMOTE:-}" ]]; then
	command -v rclone >/dev/null 2>&1 || die "rclone is required when RCLONE_REMOTE is set"
	log "uploading to $RCLONE_REMOTE"
	rclone copy --checksum "$out_file" "$RCLONE_REMOTE"
	rclone copy --checksum "$out_file.sha256" "$RCLONE_REMOTE"
fi

log "pruning local backups older than $RETENTION_DAYS days"
find "$BACKUP_DIR" -maxdepth 1 -type f \
	\( -name 'gateway-*.dump.enc' -o -name 'gateway-*.dump.enc.sha256' \) \
	-mtime "+$RETENTION_DAYS" -delete

log "backup complete"
