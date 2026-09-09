<div align="center">
  <img src="static/logo.png" alt="adassay" width="200"/>

  **An ad blocker for AI agents**

  [![Release](https://img.shields.io/github/v/release/belaytzev/adassay?label=release)](https://github.com/belaytzev/adassay/releases/latest)
  [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
  [![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux-lightgrey.svg)](https://github.com/belaytzev/adassay/releases/latest)
  [![Go](https://img.shields.io/badge/go-1.25+-00ADD8.svg)](https://go.dev/)
  [![Try it](https://img.shields.io/badge/try%20it-adassay.com-black.svg)](https://adassay.com)

  Web pages increasingly contain text written to be quoted by AI rather than read by people:
  affiliate roundups, paid inserts dressed as editorial, and instructions hidden where only a
  parser will find them. An agent reading such a page swallows all of it as fact.
  **adassay sits between the page and the agent.** It cuts what the publisher itself marked as
  commercial, flags what is doubtful, and reports text the page hides from human readers.
</div>

---

## 📖 Table of contents

- [🚀 Quick start](#-quick-start)
  - [📄 What comes out](#-what-comes-out)
  - [🤖 Give it to an agent](#-give-it-to-an-agent)
- [✨ What it actually does](#-what-it-actually-does)
  - [⚖️ How it compares](#️-how-it-compares)
- [🧰 Use it](#-use-it)
  - [💻 From the command line](#-from-the-command-line)
  - [🔌 From an AI agent](#-from-an-ai-agent)
  - [🗳️ Correcting it](#️-correcting-it)
  - [📦 What you get back](#-what-you-get-back)
- [🧠 How it decides](#-how-it-decides)
- [🔒 Privacy](#-privacy)
- [⚠️ Known limits](#️-known-limits)
- [🔬 Going deeper](#-going-deeper)
- [🤝 Contributing](#-contributing)
- [📜 Licence](#-licence)

---

## 🚀 Quick start

```sh
brew install belaytzev/tap/adassay        # macOS and Linux, amd64 and arm64
adassay https://example.com/best-laptops-2026
```

With Go 1.25 or newer, `go install adassay.com/cmd/adassay@latest` does the same. Archives for
every platform are on the [releases page](https://github.com/belaytzev/adassay/releases/latest),
and the source builds with `go build ./cmd/adassay`.

> 🌐 **No install at all:** paste a URL at **[adassay.com](https://adassay.com)** and see every
> paragraph marked kept, cut or queried, with the reasons in the margin.

### 📄 What comes out

A six-paragraph keyboard roundup goes in. This is what an agent gets back:

```markdown
Switch choice matters more than the board. Tactile switches suit people who type
all day and hate bottoming out; linears suit people who share an office …

[[adassay:flag {"id":"s2","score":0.5,"reasons":["disclaimer"]}]]
We earn commission from purchases made through the links in this article.
[[/adassay:flag]]

[[adassay:flag {"id":"s3","score":0.27,"reasons":["affiliate_link"]}]]
The Keychron Q1 is the safe recommendation: a gasket mount, a solid aluminium
case, and QMK firmware that will outlive the plastic.
[[/adassay:flag]]

Split boards look like an affectation until a wrist starts hurting …

Hot swap sockets are the feature to insist on …
```

| | Paragraphs | What happened |
|---|:---:|---|
| ✅ **Kept** | 3 | passed through untouched |
| 🏷️ **Flagged** | 2 | stayed, wrapped in a marker that says why they are doubtful |
| ✂️ **Cut** | 1 | *"Use code TYPE20 at checkout for twenty percent off"* — gone |

Nothing doubtful is removed silently — it stays, marked, and the agent decides.

### 🤖 Give it to an agent

`adassay-mcp` is an MCP server over stdio with two tools: `fetch_clean(url)` downloads a page
and returns it filtered; `check_text(text)` judges text you already have, without touching the
network.

```json
{
  "mcpServers": {
    "adassay": { "command": "adassay-mcp" }
  }
}
```

Point the agent at `fetch_clean` instead of a plain fetch tool. That is the whole integration.

---

## ✨ What it actually does

Be clear about the boundary before you install it.

- ✂️ **Reliably removes advertising the publisher declared.** Links tagged `rel="sponsored"`,
  affiliate redirectors, promo codes next to promo wording, paid-placement disclosures. On a
  labelled corpus of 52 pages and 6820 segments it removes 94% of that class and has never
  removed an honest paragraph — the one number this project refuses to trade away.
- 🕵️ **Detects text aimed at parsers rather than readers.** `display:none`, off-screen
  positioning, text painted the colour of its background, invisible unicode, prompts tucked
  into `alt` and `meta`. This is the part nothing else does, and it doubles as an indirect
  prompt-injection detector.
- 🏷️ **Never deletes what it is unsure about.** Doubtful paragraphs stay in the text with a
  marker and the reasons; the agent, or your vote, decides.
- 🔒 **Keeps what you read to itself.** Verdicts are stored as hashes; nothing leaves the
  machine unless you point it at a shared database, and even then never a word of text.
- ❌ **Does not reliably catch native advertising.** A paragraph that sells a product while
  carrying no link, no price and no disclosure reads exactly like an enthusiastic
  recommendation, and the rule layer currently catches none of it. A local model can judge the
  grey zone if you enable one, and it helps — but this is an open problem, not a solved one.
  If that class is what you need, this tool is not there yet.

### ⚖️ How it compares

| | What it removes | What it misses |
|---|---|---|
| Readability, trafilatura, Jina Reader | banners, sidebars, promo blocks — page furniture | anything inside the article body, and they *delete* the ad markup adassay needs |
| uBlock Origin, EasyList | requests and DOM nodes by URL and selector | text; they never see it |
| Prompt-injection guardrails | instructions aimed at the model | ordinary marketing prose |
| **adassay** | declared advertising inside the article, plus hidden text | undeclared native advertising |

---

## 🧰 Use it

### 💻 From the command line

```sh
adassay https://example.com/article           # filtered markdown
adassay --json https://example.com/article    # full result: segments, scores, findings
adassay --verbose https://example.com/a       # plus the hidden-text findings
cat saved.html | adassay                      # from stdin, touches no database
adassay version
```

Flags: `--config` (your own `rules.yaml`), `--db` (local database path), `--no-share` (send
nothing), `--json`, `--verbose`.

Exit codes carry meaning, so scripts and CI can act on them:

| Code | Meaning |
|:---:|---|
| `0` | ✅ page read and filtered |
| `1` | ❌ something went wrong |
| `2` | 🕵️ hidden text found — the document still prints, but the source hid something from readers |
| `3` | 🚫 the site refused the request: 403, 401, 429 or a bot wall |
| `4` | 🫥 extraction looks implausible — far more visible text on the page than was extracted, usually a JavaScript-rendered article |

### 🔌 From an AI agent

See [Give it to an agent](#-give-it-to-an-agent) above. `adassay-mcp` takes `--config` and
`--db`, and `-version` prints its version.

### 🗳️ Correcting it

Your vote overrides every layer, immediately and locally:

```sh
adassay vote https://example.com/article --ad    # everything left on this page is advertising
adassay vote <segment-sha256> --not-ad           # one segment, by hash
adassay vote "exact paragraph text" --ad         # same, hash computed on the spot
```

An argument that is neither 64 hex characters nor a URL is treated as segment text.

### 📦 What you get back

Every paragraph ends up in one of three states:

- ✅ **Keep** — passed through untouched
- 🏷️ **Flag** — kept in the text, wrapped in `[[adassay:flag {...}]] … [[/adassay:flag]]` with the reasons
- ✂️ **Drop** — removed

Doubtful content is never removed silently. It stays, marked, and the agent decides. That is
the whole design: a filter that eats facts is worse than no filter.

---

## 🧠 How it decides

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

---

## 🔒 Privacy

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

**The judge.** L3 arbitration goes to any OpenAI-compatible endpoint. `ADASSAY_JUDGE_URL` and
`ADASSAY_JUDGE_MODEL` override the built-in defaults, and `ADASSAY_JUDGE_KEY` supplies a bearer
token for gateways that want one — it is never read from `rules.yaml`, which is embedded in the
binary. A judge that cannot be reached is a warning, not a failure: the grey zone is left as the
rules decided it.

**Credentials.** An install keeps its id and secret in the user config directory. Headless
hosts can supply them through `ADASSAY_INSTALL` and `ADASSAY_SECRET` instead, which takes
precedence over the file and avoids registering a new install on every container start.

**Turning it off.** `--no-share` or `ADASSAY_NO_SHARE=1` stops sending; reading still works.
Leaving `ADASSAY_SHARE_URL` unset disables both. A run from stdin without `--db` opens no
database at all.

---

## ⚠️ Known limits

Stated plainly, because finding them yourself later is worse.

- ❌ **Undeclared native advertising is not caught.** The measured coverage of that class by the
  rule layer is zero. It is the main open problem.
- 🎨 **External CSS is invisible.** L1 reads inline styles and attributes. A class hidden by a
  linked stylesheet is not detected.
- 🫥 **JavaScript-rendered pages come back nearly empty** and exit 4. No headless browser is
  involved, by choice.
- 🚫 **Cloudflare challenges win.** Sites behind a JS challenge return exit 3. Honest headers
  cannot pass those.
- 🌍 **English and Russian only.** Pattern lists cover no other language yet, and Chinese needs a
  different matching mode entirely, since it has no word boundaries.
- 🔁 **Consensus cannot fix a systematic error.** If every client runs the same rules, a shared
  mistake gets confirmed rather than corrected. Human votes are the only counterweight.
- ⏳ **Shared-database trust is deterrence, not proof.** Registration is free; what it buys is
  time, since a fresh install carries no weight for a day and a misbehaving one loses what it
  earned. A patient attacker can still age installs.

---

## 🔬 Going deeper

Reference material for tuning the rules, running your own instances, and hosting the demo.
Nothing here is needed to use the tool.

<details>
<summary>⚙️ <strong>Configuration</strong> — your own <code>rules.yaml</code> on top of the built-in one</summary>

The built-in `internal/config/rules.yaml` is the default. Your file layers **on top**, so list
only what you change; an unknown key is an error rather than silence. Lists are replaced
whole, not appended to; `l2.weights` merges per key.

```sh
adassay --config ./my-rules.yaml https://example.com
```

#### l1 — hidden text

| Key | Meaning |
|---|---|
| `min_length` | how much text a hidden node needs before it counts |
| `long_attr_length` | length at which an attribute is considered at all |
| `imperatives` | commands aimed at an agent |
| `agent_names` | agent names an injection addresses |

Length alone is not enough for the mechanisms ordinary pages use legitimately — CMS comments,
`<template>`, `hidden`, `aria-hidden`, long attributes. Those need an imperative or an agent
name as well, or the detector fires on MediaWiki markup and plain meta descriptions.

#### l2 — advertising features

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

#### l3 — domain reputation

| Key | Meaning |
|---|---|
| `min_visits` | visits needed before a domain's history means anything |
| `half_life_days` | how fast old findings decay |
| `finding_penalty` | contribution of one finding to distrust |
| `max_shift` | largest nudge to a paragraph score (0..1) |

</details>

<details>
<summary>📐 <strong>Tuning against a corpus</strong> — <code>adassay calibrate</code> and the five numbers it reports</summary>

```sh
cd testdata/corpus && ./fetch.sh    # once: download the captured pages
adassay calibrate --corpus testdata/corpus
```

The corpus mixes synthetic fixtures, which ship with the repository, and captured pages, which
do not — those belong to their publishers, so `labels.yaml` records their URLs and `fetch.sh`
downloads them. Without them calibration still runs on the synthetic set and says what is
missing.

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

</details>

<details>
<summary>🖥️ <strong>The demo page</strong> — what runs at adassay.com, and how to host your own</summary>

A proof sheet: paste a URL, or open one of the two saved cases, and see which paragraphs were
cut, which were queried, and which were left alone, with the reasons in the margin. A toggle
switches between the page as a reader sees it and the markdown an agent actually receives.

```sh
go run ./cmd/adassay-web        # then open http://localhost:8080
```

Settings: `--addr` (default `:8080`) and `--config` for a rules override. The page, its
stylesheet, its fonts and the two case fixtures are embedded in the binary — no build step, no
node_modules, and no external request on load.

It runs `fetch → extract → pipeline` with the cache, the shared database and the judge all
switched off, so it opens no database file and sends nothing anywhere: L1 and L2 only. Fetching
goes through `fetch.GetPublic`, which refuses loopback on top of the usual private ranges, so a
hosted instance cannot be pointed at its own network, and only the default http and https ports
are dialled, on every redirect hop and not just on the address you type. `/api/analyze` is rate
limited at one request a second, burst five, keyed on the address it arrives from — on the whole
/64 for IPv6, since one client routinely holds one — and holds recent results in memory for five
minutes. No forwarded-IP header is believed, so behind a tunnel or a reverse proxy every visitor
shares one bucket: put the limit in front of it if you host it publicly. Four analyses run at
once at most, and the fifth is answered with 503 rather than queued — parsing holds memory, and
a per-address limit does not bound how many addresses there are.

To host it:

```sh
docker build --platform linux/amd64 -f Dockerfile.web -t adassay-web .
docker run -p 8080:8080 adassay-web
```

Name the platform when the build host and the cluster differ: an image built on an Apple
Silicon machine without it runs nowhere on x86 nodes, and fails with `exec format error`
rather than at build time.

It is a separate image from the server's on purpose. The demo changes often — page copy, styles,
cases — while the server holds the verdict database and should be redeployed rarely and
deliberately. A single image would tie the two together, so a change of wording would move the
database. Both binaries link sqlite either way, since the pipeline depends on the record type, so
splitting them saves about a megabyte and that is not the reason for it.

Run one replica. The rate limiter and the result cache both live in the process, so a second pod
doubles the allowance and halves the cache hit rate. The image is stateless and needs no
volume; a Deployment with a readiness probe on `/healthz` is the whole manifest.

</details>

<details>
<summary>🗄️ <strong>Running the shared database</strong> — the AGPL server, its endpoints, quorum and limits</summary>

```sh
docker build -t adassay-server .
docker run -p 8080:8080 -v adassay-data:/data adassay-server
```

The server needs one persistent volume for its database and, in production, a Postgres it can
reach; the Helm chart that runs the public instance lives with the rest of that cluster's
configuration, not here.

The server lives in `server/` as a package with `cmd/adassay-server` as a thin main, so it can
be embedded or tested directly. It is **AGPL-3.0**, unlike the rest of the repository — see
[server/README.md](server/README.md).

| Endpoint | Purpose |
|---|---|
| `POST /v1/register` | issues an install id and secret |
| `GET /v1/segments/{prefix}?norm_version=1` | returns a bucket of verdicts |
| `POST /v1/segments` | submits machine verdicts, authenticated |
| `POST /v1/vote` | submits a human vote, authenticated |
| `GET /metrics` | Prometheus |
| `GET /healthz` | liveness |

Settings: `--addr` (default `:8080`), `--db` / `ADASSAY_SERVER_DB`, `--trusted-proxies` /
`ADASSAY_TRUSTED_PROXIES`, `ADASSAY_SEEDERS`.

**Writes are authenticated.** An install registers once and presents its id and secret on
every write. Only a hash of the secret is stored, so a leaked database cannot forge writes.

**Quorum counts weight, not heads.** A verdict is not served until enough independent installs
agree. A fresh install carries zero weight for its first day, an established one carries 1,
confirmed contributions raise it to at most 4, refuted ones sink it back. Registering in bulk
buys patience, not influence. The same weighted sum decides both questions — whether a write is
accepted and whether a verdict is served — so nothing can be accepted and then not served, or
the reverse. Seeded verdicts are the one exception, and they carry their own flag rather than
a forged count; see below.

**Write limits.** One verdict per second per address, burst 256, charged per record rather
than per request. When the table of tracked addresses fills and nothing can be pruned, new
addresses are refused rather than the table being cleared — under a flood the limit tightens
instead of switching itself off.

Publish it through a tunnel rather than a port forward, and list the tunnel's addresses in
`ADASSAY_TRUSTED_PROXIES` — only then is the forwarded client IP header believed.

</details>

<details>
<summary>🌱 <strong>Seeding the database</strong> — filling a fresh instance from your own hosts</summary>

A fresh database serves nothing: quorum needs several independent installs to agree, and
until they do, every client falls back to computing verdicts locally. `adassay seed` fills it
from the operator's own hosts.

```sh
adassay seed --feeds deploy/seed/feeds.txt --since 24h --limit 60 --pause 10s
```

It walks the feeds, keeps articles published inside the window, shuffles them, and visits them
with a pause in between. Verdicts go out as `source=seed` and are published immediately,
without waiting for quorum — which is why the right to send them is granted per install:

1. register the host: `curl -X POST https://api.adassay.com/v1/register`
2. hand the id and secret to the host as `ADASSAY_INSTALL` and `ADASSAY_SECRET`
3. list the id in `ADASSAY_SEEDERS` on the server, comma-separated

The list is reconciled at every server start: an id removed from it loses the right, and
restoring the database from a dump does not hand it back. Everything else about a seeder
install is ordinary — a human vote still overrides its verdict once the vote reaches quorum.

`deploy/seed/seed.sh` is a cron wrapper for a host outside the cluster; the Helm chart in the
homelab repository runs the same binary as a CronJob. Spread the hosts across locations:
publishers rate-limit by address, and a single IP walking thirty publishers every six hours
is the pattern they block.

</details>

---

## 🤝 Contributing

The most useful contributions, in order:

- 🚀 **Use it.** Verdicts accumulate from ordinary use, with no effort required. That is the whole
  point of the design: the database grows because people run the tool, not because they annotate
  anything.
- 🗳️ **Vote when it is wrong.** `adassay vote` is the only correction that outranks every automatic
  layer, and the only defence against every client repeating the same mistake.
- 🔗 **Send affiliate hosts and redirectors.** New networks appear constantly and local ones are
  invisible from outside their market. This is a three-line change to `rules.yaml`.
- 🌍 **Add a language.** Pattern lists cover English and Russian. A native speaker adds a working
  set in minutes; guessing takes an hour and gets the nuances wrong.
- 🏷️ **Add a labelled page.** A page plus the paragraphs that are advertising is the only way to
  prove a change helped.

What stays with the maintainers: thresholds and weights, the hashing and normalisation, and
where the line between advertising and opinion falls. The first two silently break precision
and the database; the third is the project's position rather than a matter of vote.

---

## 📜 Licence

Three licences, for three different kinds of thing.

| What | Licence | Why |
|---|---|---|
| Client, CLI, MCP server, demo web UI, shared libraries | [MIT](LICENSE) | should spread without friction; every install feeds the database |
| `server/` and `cmd/adassay-server/` | [AGPL-3.0](server/LICENSE) | a closed fork of the service would take contributed verdicts and give nothing back |
| The verdict database dump | [ODbL](LICENSE-DATA) | the one asset that cannot be rewritten; improvements should return to the commons |

Using the CLI, the MCP server or the client library carries no AGPL obligation — those are
MIT. Running your own instance of the server privately carries none either; publishing
modifications is required only when you offer the service to others over a network. See
[server/README.md](server/README.md).

Test fixtures copied from third-party sites are under none of these and remain their
publishers'. The demo's fonts are third-party too: Source Serif 4 and JetBrains Mono, subset to
latin and cyrillic, under the [SIL Open Font License 1.1](internal/web/site/fonts/LICENSE) —
`adassay-web` embeds that licence text and serves it beside the fonts it covers. A subset is a
Modified Version, so the Source Serif subset ships renamed to "Adassay Serif": the OFL reserves
the name 'Source' for Adobe's own builds.
