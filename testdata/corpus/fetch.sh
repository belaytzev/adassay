#!/usr/bin/env bash
# Downloads the captured pages of the calibration corpus.
#
# The pages belong to their publishers and are not redistributed with this
# repository. This script fetches them from the URLs recorded in labels.yaml
# into this directory, where the corpus tests and `adassay calibrate` expect
# them. Synthetic fixtures (promo_*, inj_*, ctl_*) ship with the repository and
# are not touched.
#
# Usage:
#   ./fetch.sh          download what is missing
#   ./fetch.sh --force  re-download everything
#
# A page that has changed since it was labelled will fail calibration with
# "label matches no segment". That is the intended signal: the page moved on and
# its labels need revisiting.

set -uo pipefail

cd "$(dirname "$0")" || exit 1

UA='Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36'
force=0
[ "${1:-}" = "--force" ] && force=1

ok=0
skipped=0
failed=0
declare -a failures=()

while IFS=$'\t' read -r file url; do
	[ -z "$file" ] && continue
	if [ -s "$file" ] && [ "$force" -eq 0 ]; then
		skipped=$((skipped + 1))
		continue
	fi
	printf '%s ... ' "$file"
	if curl -sSLf --max-time 60 --compressed -A "$UA" "$url" -o "$file.part" 2>/dev/null; then
		mv "$file.part" "$file"
		printf 'ok (%s)\n' "$(du -h "$file" | cut -f1)"
		ok=$((ok + 1))
	else
		rm -f "$file.part"
		printf 'FAILED\n'
		failures+=("$file — $url")
		failed=$((failed + 1))
	fi
done < <(awk '
	/^  - file: / { file = $3; url = "" }
	/^    url: /  { url = $2; if (file != "" && url != "") { print file "\t" url; file = "" } }
' labels.yaml)

echo
echo "downloaded $ok, already present $skipped, failed $failed"

if [ "$failed" -gt 0 ]; then
	echo
	echo "these pages could not be fetched:"
	printf '  %s\n' "${failures[@]}"
	echo
	echo "Bot protection and dead links are both normal here. Calibration runs on"
	echo "whatever is present; a missing page is simply left out of the metrics."
	exit 1
fi
