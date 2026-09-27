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
  two-hundred-word paragraph should not weigh the same. Only pages that were read count toward
  the share; pages behind a bot wall (exit 3) or rendered by JavaScript (exit 4) are reported
  separately.

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
