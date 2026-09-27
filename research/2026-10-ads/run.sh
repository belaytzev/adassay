#!/bin/sh
# urls.txt on stdin → raw/<id>.json, .code, .err. Resumable: a page already in raw/ is skipped.
#
#   ./run.sh < urls.txt
#
# Rules only (L1 + L2): the shared database is off, the judge points at a closed port, and
# every page gets an empty database so domain reputation from one page never nudges the next.
set -eu

BIN=${ADASSAY_BIN:-adassay}
PAUSE=${PAUSE:-3}
mkdir -p raw
tmp=$(mktemp -d)
trap 'rmdir "$tmp"' EXIT

"$BIN" version > VERSION

while IFS="$(printf '\t')" read -r category query rank url; do
	id=$(printf %s "$url" | shasum | cut -c1-12)
	[ -e "raw/$id.code" ] && continue
	code=0
	env -u ADASSAY_SHARE_URL ADASSAY_NO_SHARE=1 ADASSAY_JUDGE_URL=http://127.0.0.1:9 \
		"$BIN" --json --db "$tmp/$id.db" "$url" > "raw/$id.json" 2> "raw/$id.err" || code=$?
	rm -f "$tmp/$id.db" "$tmp/$id.db-wal" "$tmp/$id.db-shm" "$tmp/$id.db-journal"
	echo "$code" > "raw/$id.code"
	echo "$code $url" >&2
	# ponytail: global pause, per-domain pacing if a publisher starts answering 429
	sleep "$PAUSE"
done
