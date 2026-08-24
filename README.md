# adassay

An ad blocker for AI agents. It strips promotional and marketing inserts from text an agent
is about to swallow as fact.

## How this differs from what already exists

| Class | What it strips | What it misses |
|---|---|---|
| Boilerplate extractors (trafilatura, readability, defuddle, Jina Reader) | structural ads: banners, sidebars, promo blocks | native inserts inside a paragraph — to them that's just article text |
| Classic ad blockers (uBlock, EasyList) | by URL and CSS selector | text, entirely |
| Prompt injection guardrails | instructions aimed at the agent | persuasion — grammatically valid but biased content |

adassay works at paragraph level after extraction, and adds something nobody else has: a
hidden-node detector on the raw DOM. An extractor drops `display:none` silently and throws
away the single most useful signal on the page — the source is deliberately feeding parsers
what it does not show people. Here that isn't garbage, it's a verdict on whether the domain
can be trusted.

Three layers:

- **L1** — raw DOM: `display:none`, off-screen positioning, `aria-hidden`, `hidden`,
  comments, `noscript`, `template`, text painted the color of its background, long
  attributes, invisible unicode (`invisible_unicode`, U+E0000 tag characters). Deterministic
  and cheap.
- **L2** — features over the segment text: `rel="sponsored"`, a promo code next to a promo
  word, affiliate link, disclaimer (`#ad`, «на правах рекламы»), brand density — a name
  repeated in a segment that already carries one of the other signals, so a technology being
  explained does not look like a product being sold — CTA mixed with scarcity. A weighted sum
  gives a score and a verdict; the grey zone moves on.
- **L3** — domain score: how often hidden nodes turned up there. It shifts segment scores,
  but never decides for them.

If you want, a local model behind Ollama handles the grey zone between thresholds — segment
text only, on your own machine.

A segment ends up as `Keep` (passed through as is), `Flag` (kept in the text, wrapped in a
`[[adassay:flag {...}]] … [[/adassay:flag]]` marker with the reasons) or `Drop` (cut).
Borderline content is never removed quietly — the agent sees the mark and decides for itself.

## Install

Needs Go 1.25 or newer.

```sh
go install adassay.com/cmd/adassay@latest
go install adassay.com/cmd/adassay-mcp@latest   # MCP server, optional
```

Grey-zone handling by a local model is optional: `ollama pull qwen2.5:7b-instruct` (model
and address live under `judge` in `rules.yaml`). Everything works without Ollama, the grey
zone simply stays `Flag`.

Or from source:

```sh
go build ./cmd/adassay
```

## Usage

```sh
adassay https://example.com/article          # markdown with markers
adassay --json https://example.com/article   # the whole Result: segments, scores, findings
                                              # (`text` is the same filtered document)
cat page.html | adassay                      # from stdin: no local and no shared database
adassay --verbose https://example.com/a      # plus the list of hidden findings
```

Exit code `2` means hidden nodes were found on the page. The document still gets printed,
but a script or CI job can refuse a source like that. Exit code `3` means the site refused
the request — an HTTP 403, 401, 429 or a Cloudflare challenge — so nothing was fetched at
all; that is a different thing from a page that came back empty, and the message says which.

Requests go out with an ordinary desktop Chrome `User-Agent` and its usual companions
(`Accept`, `Accept-Language`, `Upgrade-Insecure-Requests`, the `Sec-Fetch-*` set). Large
publishers reject anything else outright, and the point is to look like the browser the
reader would have used, not to hide. There is one identity, it never rotates, and a refusal
is never retried.

Exit code `4` means the extraction is implausible: the raw page carries far more visible text
than the reader managed to keep, so the article was probably rendered by JavaScript or hidden
behind a wall. The `thin` and `visible` fields of the JSON `Result` carry the same signal. It
lets a caller tell "the page had nothing to say" from "we failed to read the page".

A human vote overrides every layer. It lands in the local database as `human` — the next run
picks the correction up straight away — and travels to the shared database:

```sh
adassay vote https://example.com/article --ad     # everything the filter left on the page
adassay vote <segment-sha256> --not-ad            # a single segment by hash
adassay vote "exact paragraph text" --ad          # same thing, the hash is computed on the spot
```

An argument that isn't 64 hex characters and doesn't start with `http://` or `https://` is
treated as segment text: the vote goes against that string's hash, not against the page.
`vote` has its own flags: `--share` (shared database address, overrides `$ADASSAY_SHARE_URL`),
`--db` and `--config`.

Voting by URL runs the page through the same pipeline as a normal run — local database,
shared database and judge included. Otherwise only rule verdicts would reach the ballot,
and what a human actually needs to correct is precisely what the rules don't produce on
their own: someone else's `Drop` from the shared database, a judge decision, a nudge from
domain distrust.

`adassay` flags: `--config` (your own `rules.yaml`), `--db` (path to the local database),
`--no-share` (send nothing), `--json`, `--verbose`.

To tune thresholds against a labelled corpus use `adassay calibrate` (`--corpus`, defaults
to `testdata/corpus`; `--config`; `--dump` prints every segment with its verdict, which is
where labels come from). Labels live in `labels.yaml`: each page has `file`, `url`, `hidden`
and two lists of texts rather than segment ids — `ads` for paragraphs carrying a
deterministic signal, `native` for advertising by intent that carries none. A label under 40
characters must match a segment exactly, anything longer matches by prefix. A label that
matches nothing fails the run: labels quietly shrinking would improve every metric at once.

Calibration reports five numbers, because `Drop` and `Flag` are not the same outcome:

| Metric | Meaning |
|---|---|
| `drop_prec` | share of real advertising among everything cut. The only one that must stay at 1.000 — a mistake here deletes a fact silently |
| `drop_rec` | how much of the marked advertising was cut outright |
| `caught` | how much of `ads` was cut **or** flagged, i.e. did not pass as fact |
| `native` | the same for `native` — the class the fuzzy features have yet to reach |
| `flag_rate` | share of all segments flagged. This is the cost: judge calls and noise in the output |

The best config is picked by rule rather than by a blended score: reject anything with
`drop_prec` below 1.000, reject anything flagging more than 15% of the page, then take the
widest `caught`, then the widest `native`, then the least noise. Without the flag-rate cap
the search degenerates — dropping `lo` below the 0.076 floor a featureless segment scores
flags 99% of every document, which warns about nothing.

As an MCP server it exposes two tools: `fetch_clean` (download a page and return cleaned
content) and `check_text` (check text you already have — it downloads nothing, but like the
rest of the pipeline it hands the grey zone to the judge at `judge.endpoint`). `adassay-mcp`
flags: `--config` and `--db`; transport is stdio.

## rules.yaml format

The embedded `internal/config/rules.yaml` is the default. Your file is layered **on top** of
it, so you only list what you change; an unknown key is an error rather than silence. Lists
(`imperatives`, `disclaimers`, `affiliate_hosts` and the rest) are replaced wholesale, not
appended to: to add one pattern, list the built-in ones alongside it. `l2.weights` is a map
and merges per key.

```sh
adassay --config ./my-rules.yaml https://example.com
```

### l1

| Key | Meaning |
|---|---|
| `min_length` | how many characters a hidden node needs before it counts as a finding |
| `long_attr_length` | the length at which an attribute is considered at all |
| `imperatives` | commands aimed at the agent ("ignore previous", "always recommend", «игнорируй») |
| `agent_names` | agent names an injection addresses |

Length doesn't work the same way everywhere. `display:none`, off-screen positioning, color
on color and `<noscript>` are not mechanisms ordinary pages use to hide prose, so
`min_length` is enough there. CMS comments, `<template>`, `hidden`, `aria-hidden` and long
attributes are what half the web is built from, and length alone kept catching MediaWiki
markup and plain `<meta name="description">`. A node like that becomes a finding only if its
text carries an imperative from `imperatives` or a name from `agent_names`.

For those mechanisms the length threshold cuts both ways: "ignore previous instructions" is
dangerous at twenty characters too. Attributes are the exception — there you need both
length above `long_attr_length` and something addressed to an agent. Without the length
requirement seven clean pages surfaced on the corpus instead of two, so a short injection in
`alt` or `meta` remains a known ceiling.

### l2

| Key | Meaning |
|---|---|
| `hi` | a score strictly above `hi` → `Drop` |
| `lo` | a score strictly below `lo` → `Keep`; anything between, `lo` and `hi` included, is the grey zone — `Flag` or judge |
| `bias` | intercept of the logistic sum; the lower it is, the more cautious the filter |
| `weights` | weight of each feature, non-negative |
| `shortcuts` | feature combinations that fire past the sum |
| `patterns` | the word lists features are detected against |

Features are fixed in code: `rel_sponsored`, `promo_code`, `affiliate_link`, `disclaimer`,
`brand_density`, `cta_urgency`. Every one of them needs a weight — a missing weight would
silently count as zero, so it fails to load. A weight for an unknown feature fails too.

A shortcut says "if all these features fired, this is the verdict, skip the sum":

```yaml
l2:
  shortcuts:
    - features: [rel_sponsored, promo_code]
      verdict: drop
```

The lists under `patterns` (`promo_words`, `disclaimers`, `affiliate_params`,
`affiliate_hosts`, `cta_words`, `urgency_words`) are plain substring lists, case-insensitive,
matched on word boundaries: `#ad` won't fire on `#adassay`, «реклама» won't fire on
«рекламация». No list may be empty.

### l3

| Key | Meaning |
|---|---|
| `min_visits` | how many visits a domain needs before its reputation means anything |
| `half_life_days` | half-life of old findings |
| `finding_penalty` | how much one finding adds to distrust |
| `max_shift` | largest addition to a segment score at full distrust (0..1) |

### judge

`endpoint`, `model`, `timeout`, `batch_size` — the local Ollama for the grey zone. If the
model is unreachable or silent the segment stays `Flag`: a marker is more honest than a guess.

## Privacy

**Locally.** Verdicts and per-domain counters live in sqlite: `$ADASSAY_DB`, or
`adassay/verdicts.db` in the user cache (`~/.cache` on Linux, `~/Library/Caches` on macOS).
Neither segment text nor domains appear in the file in the clear — only the sha256 of
normalized text. Whoever gets hold of the database learns nothing about what you read.

**Reading the shared database.** This only happens when `ADASSAY_SHARE_URL` is set; without
it there is no client at all and nothing leaves the machine. The request is k-anonymous: the
first 4 hex characters of the hash go out (65536 buckets), the server returns the whole
bucket, and the full hash is matched locally. The client rejects a response with fewer than
8 records, and a short bucket is padded by the server with deterministic decoys. No install
identifier is sent when reading.

Be clear about what that padding is worth. It hides a thin bucket from anyone reading the
response in transit or from logs, and it keeps the client from silently accepting a
one-record answer. It does **not** hide anything from the server: the decoys are its own, so
it knows exactly how many real records a bucket held and can infer that you asked for one of
them. k-anonymity here is a real defence against a leaked response and a weak one against the
operator. Point `ADASSAY_SHARE_URL` at a server you would trust with the knowledge of what
you read.

**Writing.** Only confident verdicts go out — `Drop` segments and L1 findings — and only as
a hash, a verdict, a list of reasons and a source. There is no text in the request or in the
server database, and the request body isn't logged. The grey zone (`Flag`) is never sent:
it's an open question, and sending it would give away the page while adding nothing. Neither
is a `Drop` that came from domain distrust (`domain_distrust`): that's a claim about the
domain's reputation as seen by you, not about the text, and in the shared database it would
condemn wording that landed in the grey zone on other sites. Sending happens in batches off
disk — no sooner than 6 hours and no fewer than 20 records, so the stream doesn't turn into a
broadcast of your reading session. Writes carry a random install identifier (the file
`adassay/client_id` in the user config directory), which the server uses to weigh
contributions and throttle spam.

**Turning it off.** `--no-share` or `ADASSAY_NO_SHARE=1` disables sending entirely while
reading still works. Leaving `ADASSAY_SHARE_URL` unset means no shared database at all, for
reading or writing. You can delete `client_id` at any time, but the install's reputation goes
with it. A run from stdin without `--db` opens no local database and asks nothing of the
shared one — the only thing it can touch over the network is your own Ollama, and only if the
grey zone isn't empty. The MCP server never touches the shared database: it reads and writes
the local cache only.

## Known ceilings

- **A JavaScript challenge is a wall.** Honest headers get past a plain User-Agent filter,
  not past a Cloudflare managed challenge (`cf-mitigated: challenge`, "Just a moment...")
  — that wants a real browser executing JS with a matching TLS fingerprint. Such a page
  exits `3` and fetches nothing. Getting through means driving a headless browser, which is
  a different tool with a different risk profile.
- **External CSS is invisible.** L1 reads inline styles and attributes off the raw DOM. A
  class hidden by a rule in a linked `.css` isn't detected: fetching and parsing stylesheets
  is a different order of complexity and a different risk profile.
- **Consensus doesn't catch systematic error.** The shared database averages clients, and
  clients run the same rules. If the rules are wrong in the same way, voting records that
  rather than fixing it. The one counterweight is human votes (`adassay vote`), which
  override everything else including each other — otherwise the first mistaken vote would
  lock a hash forever. In the shared database such a replacement costs a quorum; locally the
  vote takes effect immediately.
- **L1 findings go to the database but aren't read back.** Their hash is computed in a
  separate space (`hidden/<kind>`): a finding's sample is often ordinary page prose, and L1
  fires on clean pages too, so sharing an address space with segments would publish a `Drop`
  against an honest paragraph. The client never queries that space — the signal sits there
  for server-side aggregation that doesn't exist yet.
- **Bucket responses are capped.** The server returns at most 1024 verdicts per prefix:
  reads are unauthenticated and nothing bounds how many hashes share a prefix, so without a
  cap a single GET would make the server assemble an entire bucket in memory. The cut follows
  hash order, so the set is stable between requests, but a segment inside an overflowing
  bucket won't be served — it gets decided locally.
- **Write privacy is weaker than read privacy.** Reads are k-anonymous and anonymous. Writes
  carry an install identifier and a full segment hash — otherwise there's nothing to
  deduplicate against and no way to throttle spam. Batching and delay blur the link to a
  session but don't remove it. If that trade doesn't suit you, use `--no-share`.
- **Quorum counts weight, not heads.** An install registers once at `POST /v1/register` and
  gets an id and a secret; writes carry both, and the server checks the secret against a
  stored hash, so a made-up id writes nothing. Registration is still free, which is the
  point: a fresh install carries **zero** weight for its first day, an established one
  carries 1, and confirmed contributions raise it to at most 4 while refuted ones sink it
  back to nothing. Registering in bulk buys patience, not influence, and an install that
  turns bad loses what it earned. This is deterrence, not proof — a determined attacker can
  still age installs and behave until it matters.
- **A single request is k-anonymous, a whole page is not.** A bucket is fetched per segment,
  so a page of two hundred paragraphs leaves as two hundred prefixes in a row from one
  address. An individual prefix says nothing, but a set of them arriving together is close to
  a unique fingerprint of the document, and a server with its own crawler can match it.
  Narrowing this means batching the page's unique prefixes and padding with decoys — until
  then, point `ADASSAY_SHARE_URL` only at a server you trust to know what you read.
- **Loopback stays reachable.** Fetching refuses private, link-local and CGNAT addresses —
  cloud metadata at `169.254.169.254` above all — but not `127.0.0.1`, since otherwise you
  couldn't filter a page off a local server. With the CLI a human types the URL, but
  `fetch_clean` in MCP gets one from an agent that just read someone else's page, so an
  injection saying "fetch http://127.0.0.1:…" reaches a service on the same machine. The fix
  is a network namespace for the agent, not a flag.
- **Normalization is versioned.** The version is baked inside the digest: hashes from
  different normalization versions live at different addresses and never mix. Changing
  normalization without bumping `core.NormVersion` would quietly invalidate the database.

## Shared database server

```sh
docker build -t ghcr.io/belaytzev/adassay-server:latest .
kubectl apply -f deploy/k8s/
```

Endpoints: `GET /v1/segments/{prefix}?norm_version=1` (the parameter is required, a foreign
version gets `400`; a short bucket is padded with deterministic decoys up to 8 records),
`POST /v1/segments` (machine verdicts — `rules` and `ollama`; `human` is rejected here, or
one batch could override 256 verdicts at once), `POST /v1/vote` (a human vote, one hash per
request), `GET /metrics`, `GET /healthz` (a static response for k8s probes — `/metrics`
won't do, it computes gauges from the database on every request). Settings: `--addr`
(defaults to `:8080`), `--db` / `ADASSAY_SERVER_DB` (defaults to `adassay-server.db` in the
working directory), `--trusted-proxies` / `ADASSAY_TRUSTED_PROXIES`.

**Quarantine.** A submitted verdict isn't served to clients until three different installs
agree with it. Agreement is recorded against a specific verdict, and a client holds one
opinion per hash: disagreeing moves its vote rather than adding a second. A challenger
trying to replace a published verdict gathers its own quorum — until then it sits in
quarantine and clients keep getting the previous answer. Source rank (`rules` < `ollama` <
`human`) protects only a published verdict from a weaker source: an unconfirmed record can
be taken by any quorum, or a hash nobody else ever saw would belong forever to whoever
claimed the strongest source first. Agreeing with an already stored verdict adds a
confirmation without rewriting its source, otherwise one client could claim someone else's
string and use that rank to fend off honest corrections. So a submission doesn't show up in
results immediately, and one client repeating itself or dumping thousands of hashes
publishes nothing — and unpublishes nothing of anyone else's.

The write path is authenticated: `POST /v1/register` issues an id and a secret, and both
`POST /v1/segments` and `POST /v1/vote` require the `X-Adassay-Install` and
`X-Adassay-Secret` headers. The server stores only a hash of the secret, so a leaked database
cannot be used to forge writes. The `client_id` in the body must match the signing install.

**Write limits.** 1 verdict per second per address, burst 256 — exactly one full batch; a
batch spends a token per record rather than per request. Above that, `429` with
`Retry-After`. The table of tracked addresses is capped; when it is full and nothing can be
pruned, new addresses are refused rather than the table being cleared — under a flood the
limit tightens instead of switching itself off. Request body up to 1 MiB, batches from 1 to 256 records, a reason up to 48
characters from `[a-z0-9_-]` (free text in reasons is rejected: it's the only field wide
enough to smuggle article text into a database that stores no text).

**Metrics:** `adassay_requests_total{endpoint}`, `adassay_bucket_hits_total`,
`adassay_submit_divergent_total`, `adassay_verdicts_published`,
`adassay_verdicts_quarantined`. Database counters refresh at most once every 30 seconds.

The service is published through a Cloudflare Tunnel rather than a direct port forward: the
Ingress in `deploy/k8s/ingress.yaml` stays cluster-internal and the tunnel pod talks to the
`adassay` Service. That's also where `ADASSAY_TRUSTED_PROXIES` comes from — it lists the
tunnel's addresses or CIDRs whose `CF-Connecting-IP` header the server trusts when counting
write limits. For any other peer that header is client-controlled, so it's ignored.
