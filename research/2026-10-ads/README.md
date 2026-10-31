# How much of what an agent reads is declared advertising

The data behind [adassay.com/report](https://adassay.com/report).

## Method

- **Queries.** 60 in `queries.txt`: ten each in five categories where people ask an agent to
  pick a product (electronics, software, hosting, home, finance), plus ten `control` queries
  with no product in them.
- **Pages.** The top ten Google results for each query, through SearXNG's `google cse` engine,
  deduplicated across queries: 593 URLs in `urls.txt`. The list is frozen — a rerun reads these
  pages, not today's results.
- **Reading.** `adassay --json`, one page at a time, rules only: no shared database, no model
  judge, a fresh local database per page so one site's reputation never nudges the next.
  The version is in `VERSION`. Fetched from a residential address in Spain; a second pass from
  a Finnish datacenter read the same share of pages, only Reddit answered it with 403 instead of
  an empty JavaScript shell.
- **Counting.** Share of *characters*, not paragraphs: an eight-word promo line and a
  two-hundred-word paragraph should not weigh the same. A page counts as read when it returned
  any text (`segments > 0` in `pages.tsv`), whatever the exit code: 40 pages with exit 4 returned
  part of their text and are counted. The 236 that returned none are reported separately.

## What the numbers are not

- **A floor.** adassay cuts advertising the publisher itself declared — `rel="sponsored"`,
  affiliate redirectors, promo codes, disclosures. Native advertising with none of those
  markers is not counted at all.
- **One search engine, one day, one place.** An agent using a different search API sees
  different pages.
- **English only.**

## Rerun it

```sh
./run.sh < urls.txt              # needs adassay on PATH; resumable, writes raw/
./aggregate.sh < urls.txt        # pages.tsv, segments.tsv, summary on stdout
./collect.sh < queries.txt       # only to draw a fresh sample; needs a SearXNG instance
```

`raw/` holds the page text and is not published: it belongs to the publishers. `pages.tsv` and
`segments.tsv` carry lengths, verdicts, scores and reasons, never text.

`drop_review.tsv` is the precision check: 100 dropped segments drawn at random (`srand(42)`),
each labelled by hand as `ad`, `chrome` (page furniture), `pitch`, or `fact` — a line that
should not have been cut.

## Judge probe

The rules leave a grey zone: segments they `flag` but are not sure enough to cut. A model judge
decides those. `judge_probe.tsv` is the evidence for its thresholds (`judge.drop_at`,
`judge.keep_at`): every flagged segment of this dataset — 488 segments on 46 pages — put to two
judges on 2026-10-31.

- **Jev** (`jev-1.14.0`, the `systemone` judge in `internal/judge`) answers each segment with
  `p`, the probability that it exists to sell rather than to inform. `p1` and `p2` are two
  identical requests per page from `judge_probe/probe.sh`, which replicates the judge's request,
  capped at 40 segments per page: the 40 segments beyond the cap have no `p`. `jev` is the
  verdict of the judge code itself at `drop_at` 0.8 and `keep_at` 0.25, run separately.
- **luna** (`gpt-5.6-luna-adassay`, the earlier OpenAI-compatible judge) answers `drop` or `keep`.

`label` is the ground truth, set by a person reading the segment in `raw/`, never by a model: the
judges are what is being measured. `ad` — the segment exists to sell; `info` — it informs, even
when enthusiastic or carrying a shop link. A segment the labeller cannot decide stays empty.

```sh
./judge_probe.sh > judge_probe.tsv     # rebuild from raw/ and judge_probe/; overwrites the labels
judge_probe/probe.sh raw judge_probe/run3   # a fresh Jev pass; needs ADASSAY_JUDGE_{URL,KEY,MODEL}
```

Thresholds from the labels: for every candidate `t`, the share of `ad` among segments with mean
`p` ≥ `t` and of `info` among those with `p` ≤ `t`. `drop_at` is the lowest `t` whose drop
precision holds the target, `keep_at` the highest `t` whose keep precision does.

```sh
duckdb -c "
WITH l AS (SELECT (p1 + p2) / 2 AS p, label FROM read_csv('judge_probe.tsv', delim = '\t', header = true)
           WHERE label <> '' AND p1 IS NOT NULL),
t AS (SELECT DISTINCT p AS t FROM l)
SELECT t,
  count(*) FILTER (WHERE p >= t) AS drop_n,
  round(avg((label = 'ad')::int) FILTER (WHERE p >= t), 2) AS drop_precision,
  count(*) FILTER (WHERE p <= t) AS keep_n,
  round(avg((label = 'info')::int) FILTER (WHERE p <= t), 2) AS keep_precision
FROM l, t GROUP BY t ORDER BY t"
```
