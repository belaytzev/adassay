#!/bin/sh
# Registers one seeding install and writes its credentials to an env file.
#
#   ./register.sh https://api.adassay.com /etc/adassay/seed.env
#
# The file is created at 0600 by this script rather than by a shell redirect:
# a redirect creates it at the caller's umask, so the seeder secret would land
# in a world-readable file and any local account could forge published verdicts.
#
# The id it prints still has to be listed in ADASSAY_SEEDERS on the server,
# otherwise the server refuses source=seed from it.
set -eu

URL=${1:-${ADASSAY_SHARE_URL:-}}
OUT=${2:-${ADASSAY_SEED_ENV:-/etc/adassay/seed.env}}
: "${URL:?usage: register.sh <server url> [env file]}"

if [ -e "$OUT" ]; then
	echo "register: $OUT exists, refusing to overwrite credentials" >&2
	exit 1
fi

body=$(curl -fsS -X POST "$URL/v1/register")
id=$(printf '%s' "$body" | jq -re .client_id)
secret=$(printf '%s' "$body" | jq -re .secret)

umask 077
mkdir -p "$(dirname "$OUT")"
cat > "$OUT" <<ENV
ADASSAY_SHARE_URL=$URL
ADASSAY_INSTALL=$id
ADASSAY_SECRET=$secret
ENV
chmod 600 "$OUT"

echo "registered $id -> $OUT, add the id to ADASSAY_SEEDERS on the server" >&2
