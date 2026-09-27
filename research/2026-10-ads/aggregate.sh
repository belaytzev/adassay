#!/bin/sh
# urls.txt on stdin + raw/ → pages.tsv, segments.tsv, and the summary on stdout.
# No segment text leaves raw/: the published dataset carries lengths, verdicts and reasons.
#
#   ./aggregate.sh < urls.txt > summary.txt
set -eu

printf 'id\tcategory\tquery\trank\tdomain\texit\tsegments\tchars\tchars_drop\tchars_flag\n' > pages.tsv
printf 'id\tseg\tverdict\tscore\tchars\treasons\n' > segments.tsv

while IFS="$(printf '\t')" read -r category query rank url; do
	id=$(printf %s "$url" | shasum | cut -c1-12)
	[ -e "raw/$id.code" ] || continue
	code=$(cat "raw/$id.code")
	domain=$(printf %s "$url" | awk -F/ '{sub(/^www\./, "", $3); print $3}')
	if jq -e '.segments | length > 0' "raw/$id.json" > /dev/null 2>&1; then
		jq -r --arg id "$id" '.segments[] | [$id, .id, .verdict, (.score * 1000 | round / 1000), (.text | length), ((.reasons // []) | join(","))] | @tsv' \
			"raw/$id.json" >> segments.tsv
		stats=$(jq -r '[(.segments | length),
			([.segments[].text | length] | add),
			([.segments[] | select(.verdict == "drop") | .text | length] | add // 0),
			([.segments[] | select(.verdict == "flag") | .text | length] | add // 0)] | @tsv' "raw/$id.json")
	else
		stats=$(printf '0\t0\t0\t0')
	fi
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$id" "$category" "$query" "$rank" "$domain" "$code" "$stats" >> pages.tsv
done

echo "== by category: read pages, pages with a drop, share of text dropped / flagged, unreadable"
awk -F'\t' 'NR > 1 {
	c = $2; all[c]++
	if ($7 == 0) { dead[c]++; next }
	read[c]++; ch[c] += $8; dr[c] += $9; fl[c] += $10; if ($9 > 0) hit[c]++
	ch["ALL"] += $8; dr["ALL"] += $9; fl["ALL"] += $10; read["ALL"]++; if ($9 > 0) hit["ALL"]++
} END {
	for (c in all) { dead["ALL"] += dead[c]; all["ALL"] += all[c] }
	printf "%-12s %5s %6s %8s %8s %10s\n", "category", "read", "w/drop", "drop%", "flag%", "unreadable"
	for (c in all) printf "%-12s %5d %5.0f%% %7.1f%% %7.1f%% %9.0f%%\n", c, read[c], 100 * hit[c] / (read[c] ? read[c] : 1),
		100 * dr[c] / (ch[c] ? ch[c] : 1), 100 * fl[c] / (ch[c] ? ch[c] : 1), 100 * dead[c] / all[c]
}' pages.tsv | sort

echo "== exit codes"
awk -F'\t' 'NR > 1 { n[$6]++ } END { for (k in n) print k, n[k] }' pages.tsv | sort

echo "== dropped text by reason set"
awk -F'\t' 'NR > 1 && $3 == "drop" { n[$6] += $5; t += $5 } END { for (k in n) printf "%5.1f%% %s\n", 100 * n[k] / t, k }' segments.tsv | sort -rn | head -15

echo "== top domains by dropped text"
awk -F'\t' 'NR > 1 && $9 > 0 { d[$5] += $9; t += $9 } END { for (k in d) printf "%5.1f%% %s\n", 100 * d[k] / t, k }' pages.tsv | sort -rn | head -10
