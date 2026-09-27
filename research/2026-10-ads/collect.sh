#!/bin/sh
# queries.txt (category<TAB>query) on stdin → urls.txt (category, query, rank, url) on stdout.
# Run once; urls.txt is committed and every rerun starts from it, not from today's results.
#
#   ./collect.sh < queries.txt > urls.txt
set -eu

SEARX=${SEARX:-https://search.t1go.net/search}
TOP=${TOP:-10}
seen=$(mktemp)
trap 'rm -f "$seen"' EXIT

while IFS="$(printf '\t')" read -r category query; do
	[ -n "$query" ] || continue
	curl -fsS --max-time 30 -G "$SEARX" \
		--data-urlencode "q=$query" --data-urlencode 'engines=google cse' \
		-d format=json -d language=en |
		jq -r --arg top "$TOP" '.results[:($top|tonumber)] | to_entries[] | "\(.key + 1)\t\(.value.url)"' |
		while IFS="$(printf '\t')" read -r rank url; do
			grep -qxF "$url" "$seen" && continue
			echo "$url" >> "$seen"
			printf '%s\t%s\t%s\t%s\n' "$category" "$query" "$rank" "$url"
		done
	sleep 2
done
