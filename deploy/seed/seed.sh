#!/bin/sh
# Runs one seeding pass. Meant for cron on a host outside the cluster:
#
#   17 */6 * * *  /opt/adassay/seed.sh >> /var/log/adassay-seed.log 2>&1
#
# Reads its settings from /etc/adassay/seed.env:
#
#   ADASSAY_SHARE_URL=https://api.adassay.com
#   ADASSAY_INSTALL=<install id>
#   ADASSAY_SECRET=<install secret>
#
# The install must be marked as a seeder on the server, otherwise the submit
# is refused. Register it once with `adassay share --register`, then flip the
# flag in the database.
set -eu

ENV_FILE=${ADASSAY_SEED_ENV:-/etc/adassay/seed.env}
BIN=${ADASSAY_BIN:-/opt/adassay/adassay}
FEEDS=${ADASSAY_FEEDS:-/opt/adassay/feeds.txt}
DB=${ADASSAY_DB:-/var/lib/adassay/seed.db}
LOCK=${ADASSAY_LOCK:-/var/lock/adassay-seed}

[ -r "$ENV_FILE" ] || { echo "seed: no $ENV_FILE" >&2; exit 1; }
. "$ENV_FILE"
export ADASSAY_SHARE_URL ADASSAY_INSTALL ADASSAY_SECRET

: "${ADASSAY_SHARE_URL:?set in $ENV_FILE}"
: "${ADASSAY_INSTALL:?set in $ENV_FILE}"
: "${ADASSAY_SECRET:?set in $ENV_FILE}"

run() {
	echo "seed: $(date -u +%FT%TZ) starting on $(hostname)"
	"$BIN" seed \
		--feeds "$FEEDS" \
		--db "$DB" \
		--since "${ADASSAY_SINCE:-24h}" \
		--limit "${ADASSAY_LIMIT:-60}" \
		--pause "${ADASSAY_PAUSE:-8s}"
}

# One pass at a time: a run can outlive its cron slot, and two crawlers from one
# IP is exactly the pattern publishers block. -E gives a busy lock its own code,
# so a failing pass is not mistaken for a concurrent one and still exits nonzero
# — cron and exit-status monitoring are the only thing watching this.
if [ -z "${ADASSAY_LOCKED:-}" ] && command -v flock >/dev/null 2>&1; then
	ADASSAY_LOCKED=1
	export ADASSAY_LOCKED
	code=0
	flock -n -E 99 "$LOCK" "$0" "$@" || code=$?
	if [ "$code" -eq 99 ]; then
		echo "seed: another pass is already running" >&2
		exit 0
	fi
	exit "$code"
fi
run
