# adassay

An ad blocker for AI agents.

Web pages increasingly contain text written to be quoted by AI rather than read by people:
affiliate roundups, paid inserts dressed as editorial, and instructions hidden where only a
parser will find them. An agent reading such a page swallows all of it as fact.

adassay sits between the page and the agent. It cuts what the publisher itself marked as
commercial, flags what is doubtful, and reports text the page hides from human readers.

```sh
adassay https://example.com/best-laptops-2026
```

## What it actually does

Be clear about the boundary before you install it.

**It reliably removes advertising the publisher declared.** Links tagged `rel="sponsored"`,
affiliate redirectors, promo codes next to promo wording, paid-placement disclosures. On a
labelled corpus of 52 pages and 6820 segments it removes 94% of that class and has never
removed an honest paragraph — the one number this project refuses to trade away.

**It detects text aimed at parsers rather than readers.** `display:none`, off-screen
positioning, text painted the colour of its background, invisible unicode, prompts tucked
into `alt` and `meta`. This is the part nothing else does, and it doubles as an indirect
prompt-injection detector.

**It does not reliably catch native advertising.** A paragraph that sells a product while
carrying no link, no price and no disclosure reads exactly like an enthusiastic
recommendation, and the rule layer currently catches none of it. A local model can judge the
grey zone if you enable one, and it helps — but this is an open problem, not a solved one.
If that class is what you need, this tool is not there yet.

## How it compares

| | What it removes | What it misses |
|---|---|---|
| Readability, trafilatura, Jina Reader | banners, sidebars, promo blocks — page furniture | anything inside the article body, and they *delete* the ad markup adassay needs |
| uBlock Origin, EasyList | requests and DOM nodes by URL and selector | text; they never see it |
| Prompt-injection guardrails | instructions aimed at the model | ordinary marketing prose |
| **adassay** | declared advertising inside the article, plus hidden text | undeclared native advertising |

## Install

Go 1.25 or newer.

```sh
go install adassay.com/cmd/adassay@latest
go install adassay.com/cmd/adassay-mcp@latest   # MCP server, optional
```

From source:

```sh
git clone https://git.t1go.net/belaytzev/adassay
cd adassay && go build ./cmd/adassay
```

## Use it

### From the command line

```sh
adassay https://example.com/article           # filtered markdown
adassay --json https://example.com/article    # full result: segments, scores, findings
adassay --verbose https://example.com/a       # plus the hidden-text findings
cat saved.html | adassay                      # from stdin, touches no database
```

Flags: `--config` (your own `rules.yaml`), `--db` (local database path), `--no-share` (send
nothing), `--json`, `--verbose`.

Exit codes carry meaning, so scripts and CI can act on them:

| Code | Meaning |
|---|---|
| 0 | page read and filtered |
| 1 | something went wrong |
| 2 | hidden text found — the document still prints, but the source hid something from readers |
| 3 | the site refused the request: 403, 401, 429 or a bot wall |
| 4 | extraction looks implausible — far more visible text on the page than was extracted, usually a JavaScript-rendered article |

### From an AI agent

The MCP server exposes two tools:

- `fetch_clean(url)` — download a page and return it filtered
- `check_text(text)` — judge text you already have, no network

```sh
adassay-mcp        # stdio transport; flags: --config, --db
```

Add it to your agent's MCP configuration and point the agent at `fetch_clean` instead of a
plain fetch tool.

### Correcting it

Your vote overrides every layer, immediately and locally:

```sh
adassay vote https://example.com/article --ad    # everything left on this page is advertising
adassay vote <segment-sha256> --not-ad           # one segment, by hash
adassay vote "exact paragraph text" --ad         # same, hash computed on the spot
```

An argument that is neither 64 hex characters nor a URL is treated as segment text.

## What you get back

Every paragraph ends up in one of three states:

- **Keep** — passed through untouched
- **Flag** — kept in the text, wrapped in `[[adassay:flag {...}]] … [[/adassay:flag]]` with the reasons
- **Drop** — removed

Doubtful content is never removed silently. It stays, marked, and the agent decides. That is
the whole design: a filter that eats facts is worse than no filter.

## How it decides

Three layers, cheapest first.

**L1 — hidden text, on the raw DOM.** Runs before the article is cleaned, because extractors
throw hidden nodes away and with them the most telling signal on the page: the source is
feeding parsers something it does not show people. A finding is not a paragraph to delete —
it is evidence about the domain.

To avoid screaming at every site with a screen-reader label, a node only counts when it is
long enough, or carries an imperative ("ignore previous", "always recommend"), or names an
agent.

**L2 — features over each paragraph.** `rel="sponsored"`, promo code beside promo wording,
affiliate link, disclosure, brand density, CTA with scarcity. A weighted sum produces a score;
above the upper threshold it is dropped, below the lower one kept, in between it is doubtful.

Two of these deserve a note. Brand density only fires when another signal already fired —
otherwise it flagged every article that repeats a technical term, and "Kubernetes" in a
Kubernetes tutorial looked exactly like a product being pushed. And links are read from the
**raw** DOM, because readability prunes blocks by link density, which is a fair description
of advertising: the markup L2 depends on was being deleted before L2 ran.

**L3 — domain reputation.** How often hidden text turned up on this domain, decayed over
time. It nudges scores; it never decides alone.

**The judge, optional.** Doubtful paragraphs can go to a local model over an OpenAI-compatible
API — LM Studio, Ollama, vLLM, llama.cpp all work. It is asked about function, not tone: does
this fragment exist to inform the reader or to sell them something? A sincere recommendation
written with enthusiasm stays; a paid insert in flat engineering prose goes.

```yaml
judge:
  endpoint: http://127.0.0.1:1234
  model: qwen/qwen3.5-9b
  timeout: 60s
  batch_size: 8
```

Without a model running, doubtful paragraphs simply stay flagged. Nothing else changes.

## Privacy

**Locally.** Verdicts live in sqlite under `$ADASSAY_DB` or the user cache directory. Neither
page text nor domains are stored in the clear — only SHA-256 of normalised text. Someone who
takes the database learns nothing about what you read.

**Reading the shared database.** Only when `ADASSAY_SHARE_URL` is set; without it no client
exists and nothing leaves the machine. Requests are k-anonymous: four hex characters of the
hash go out, the server returns a whole bucket, the match happens locally.

Be clear about what that is worth. Short buckets are padded with decoys, which protects a
response read in transit — but the decoys are the server's own, so **the operator can still
tell how many real records a bucket held**. Point `ADASSAY_SHARE_URL` at a server you would
trust with the knowledge of what you read.

**Writing.** Only confident verdicts go out, and only as hash, verdict, reasons and source.
No text, ever, in the request or in the server's database. Doubtful paragraphs are never sent:
they are an open question, and sending one would give away the page while adding nothing.
Sending happens in batches off disk — not sooner than six hours, not fewer than twenty
records — so the stream is not a broadcast of your reading session.

**Turning it off.** `--no-share` or `ADASSAY_NO_SHARE=1` stops sending; reading still works.
Leaving `ADASSAY_SHARE_URL` unset disables both. A run from stdin without `--db` opens no
database at all.

## Known limits

Stated plainly, because finding them yourself later is worse.

- **Undeclared native advertising is not caught.** The measured coverage of that class by the
  rule layer is zero. It is the main open problem.
- **External CSS is invisible.** L1 reads inline styles and attributes. A class hidden by a
  linked stylesheet is not detected.
- **JavaScript-rendered pages come back nearly empty** and exit 4. No headless browser is
  involved, by choice.
- **Cloudflare challenges win.** Sites behind a JS challenge return exit 3. Honest headers
  cannot pass those.
- **English and Russian only.** Pattern lists cover no other language yet, and Chinese needs a
  different matching mode entirely, since it has no word boundaries.
- **Consensus cannot fix a systematic error.** If every client runs the same rules, a shared
  mistake gets confirmed rather than corrected. Human votes are the only counterweight.
- **Shared-database trust is deterrence, not proof.** Registration is free; what it buys is
  time, since a fresh install carries no weight for a day and a misbehaving one loses what it
  earned. A patient attacker can still age installs.

## Configuration

The built-in `internal/config/rules.yaml` is the default. Your file layers **on top**, so list
only what you change; an unknown key is an error rather than silence. Lists are replaced
whole, not appended to; `l2.weights` merges per key.

```sh
adassay --config ./my-rules.yaml https://example.com
```

### l1 — hidden text

| Key | Meaning |
|---|---|
| `min_length` | how much text a hidden node needs before it counts |
| `long_attr_length` | length at which an attribute is considered at all |
| `imperatives` | commands aimed at an agent |
| `agent_names` | agent names an injection addresses |

Length alone is not enough for the mechanisms ordinary pages use legitimately — CMS comments,
`<template>`, `hidden`, `aria-hidden`, long attributes. Those need an imperative or an agent
name as well, or the detector fires on MediaWiki markup and plain meta descriptions.

### l2 — advertising features

| Key | Meaning |
|---|---|
| `hi` | above this score a paragraph is dropped |
| `lo` | below it, kept; between them, doubtful |
| `bias` | intercept of the logistic sum; lower is more cautious |
| `weights` | per-feature weight, non-negative, all six required |
| `shortcuts` | feature combinations that decide without the sum |
| `patterns` | the word lists features match against |

Features are fixed in code: `rel_sponsored`, `promo_code`, `affiliate_link`, `disclaimer`,
`brand_density`, `cta_urgency`. A missing weight fails to load rather than counting as zero.

```yaml
l2:
  shortcuts:
    - features: [rel_sponsored, promo_code]
      verdict: drop
```

### l3 — domain reputation

| Key | Meaning |
|---|---|
| `min_visits` | visits needed before a domain's history means anything |
| `half_life_days` | how fast old findings decay |
| `finding_penalty` | contribution of one finding to distrust |
| `max_shift` | largest nudge to a paragraph score (0..1) |

## Tuning against a corpus

```sh
adassay calibrate --corpus testdata/corpus
```

Labels live in `labels.yaml`: per page, a `file`, a `url`, whether the page has `hidden` text,
and two lists of paragraph texts — `ads` for declared advertising, `native` for advertising by
intent that carries no signal. A label matching nothing fails the run, because labels quietly
shrinking would improve every metric at once.

Five numbers come back, because dropping and flagging are not the same outcome:

| Metric | Meaning |
|---|---|
| `drop_prec` | share of real advertising among everything cut — **must stay 1.000**, anything less means a fact was deleted |
| `drop_rec` | how much declared advertising was cut outright |
| `caught` | how much was cut **or** flagged, i.e. did not pass as fact |
| `native` | the same for undeclared native advertising — currently 0.000 |
| `flag_rate` | share of all paragraphs flagged; this is the cost in judge calls and noise |

The best configuration is chosen by rule, not by a blended score: reject anything with
`drop_prec` below 1.000, reject anything flagging more than 15% of a page, then take the
widest coverage, then the least noise.

## Running the shared database

```sh
docker build -t adassay-server .
kubectl apply -f deploy/k8s/
```

| Endpoint | Purpose |
|---|---|
| `POST /v1/register` | issues an install id and secret |
| `GET /v1/segments/{prefix}?norm_version=1` | returns a bucket of verdicts |
| `POST /v1/segments` | submits machine verdicts, authenticated |
| `POST /v1/vote` | submits a human vote, authenticated |
| `GET /metrics` | Prometheus |
| `GET /healthz` | liveness |

Settings: `--addr` (default `:8080`), `--db` / `ADASSAY_SERVER_DB`, `--trusted-proxies` /
`ADASSAY_TRUSTED_PROXIES`.

**Writes are authenticated.** An install registers once and presents its id and secret on
every write. Only a hash of the secret is stored, so a leaked database cannot forge writes.

**Quorum counts weight, not heads.** A verdict is not served until enough independent installs
agree. A fresh install carries zero weight for its first day, an established one carries 1,
confirmed contributions raise it to at most 4, refuted ones sink it back. Registering in bulk
buys patience, not influence.

**Write limits.** One verdict per second per address, burst 256, charged per record rather
than per request. When the table of tracked addresses fills and nothing can be pruned, new
addresses are refused rather than the table being cleared — under a flood the limit tightens
instead of switching itself off.

Publish it through a tunnel rather than a port forward, and list the tunnel's addresses in
`ADASSAY_TRUSTED_PROXIES` — only then is the forwarded client IP header believed.

## Contributing

The most useful contributions, in order:

**Use it.** Verdicts accumulate from ordinary use, with no effort required. That is the whole
point of the design: the database grows because people run the tool, not because they annotate
anything.

**Vote when it is wrong.** `adassay vote` is the only correction that outranks every automatic
layer, and the only defence against every client repeating the same mistake.

**Send affiliate hosts and redirectors.** New networks appear constantly and local ones are
invisible from outside their market. This is a three-line change to `rules.yaml`.

**Add a language.** Pattern lists cover English and Russian. A native speaker adds a working
set in minutes; guessing takes an hour and gets the nuances wrong.

**Add a labelled page.** A page plus the paragraphs that are advertising is the only way to
prove a change helped.

What stays with the maintainers: thresholds and weights, the hashing and normalisation, and
where the line between advertising and opinion falls. The first two silently break precision
and the database; the third is the project's position rather than a matter of vote.

## Licence

Code is [MIT](LICENSE). The verdict database is [ODbL](LICENSE-DATA) — permissive code so it
spreads, copyleft data so contributed verdicts stay in the commons.

Test fixtures copied from third-party sites are neither, and remain their publishers'.
