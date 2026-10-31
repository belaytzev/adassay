#!/bin/sh
# raw/ + judge_probe/ → judge_probe.tsv. No segment text leaves raw/.
#
#   ./judge_probe.sh > judge_probe.tsv
set -eu
d=judge_probe

printf 'id\tseg\tchars\treasons\tp1\tp2\tjev\tluna\tlabel\n'
{
	for run in 1 2; do
		for f in "$d"/run$run/*.resp; do
			jq -r --arg r "p$run" --arg id "$(basename "$f" .resp)" \
				'.answers | to_entries[] | [$r, $id, .key, .value.noul] | @tsv' "$f"
		done
	done
	awk '{ print "luna\t" $0 }' "$d/luna.tsv"
	for id in $(cut -f1 "$d/jev.tsv" | uniq); do
		jq -r --arg id "$id" '.segments[] | ["raw", $id, .id, (.text | length), ((.reasons // []) | join(","))] | @tsv' \
			"raw/$id.json"
	done
	awk '{ print "jev\t" $0 }' "$d/jev.tsv"
} | awk -F'\t' -v OFS='\t' '
$1 == "p1" || $1 == "p2" { p[$1, $2, $3] = $4; next }
$1 == "luna" { luna[$2, $3] = $4; next }
$1 == "raw" { chars[$2, $3] = $4; reasons[$2, $3] = $5; next }
{
	k = $2 SUBSEP $3
	if (!(k in chars)) { print "no raw segment " $2 " " $3 > "/dev/stderr"; exit 1 }
	print $2, $3, chars[k], reasons[k], p["p1", k], p["p2", k], $4, luna[k], ""
}'
