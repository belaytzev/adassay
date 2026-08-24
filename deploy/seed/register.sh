#!/bin/sh
# Registers one seeding install and writes its credentials as an env file.
#
#   ./register.sh https://api.adassay.com > /etc/adassay/seed.env
#
# The id it prints still has to be listed in ADASSAY_SEEDERS on the server,
# otherwise the server refuses source=seed from it.
set -eu

URL=${1:-${ADASSAY_SHARE_URL:-}}
: "${URL:?usage: register.sh <server url>}"

body=$(curl -fsS -X POST "$URL/v1/register")
id=$(printf '%s' "$body" | jq -re .client_id)
secret=$(printf '%s' "$body" | jq -re .secret)

cat <<ENV
ADASSAY_SHARE_URL=$URL
ADASSAY_INSTALL=$id
ADASSAY_SECRET=$secret
ENV
echo "registered $id, add it to ADASSAY_SEEDERS on the server" >&2
