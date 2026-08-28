// Page text is attacker-influenced: render.Safe defuses adassay markers but does
// not escape HTML, so every string from the API goes in through textContent.
"use strict";

const LABEL = { drop: "cut", flag: "query" };
const CLASS = { drop: "seg--cut", flag: "seg--query", keep: "" };

const ERRORS = {
  blocked:
    "The site refused the request. adassay asks with ordinary browser headers and does not " +
    "pretend to be anything else, so a Cloudflare wall, a paywall or a plain 403 stops it here. " +
    "Nothing on this page can talk its way past that.",
  thin:
    "Far more visible text on that page than came out of it — usually an article that assembles " +
    "itself in JavaScript. adassay reads HTML and ships no headless browser, which is a decision " +
    "rather than a missing feature. What is below is everything that could be extracted.",
  invalid: "That is not an http or https address.",
  failed: "The page could not be read.",
  rate_limited:
    "Too many pages from this address just now. Wait a second — or install it and read " +
    "as many as you like, with no limit and no server in the middle.",
  busy:
    "The demo is reading as many pages at once as it will hold. Try again in a moment — " +
    "or install it, where the only queue is yours.",
};

const EMPTY =
  "Nothing came out of that page. adassay reads articles out of HTML: a PDF, a feed, " +
  "an image or a page that is only markup gives it nothing to work with.";

let current = null;
let view = "human";

const el = (id) => document.getElementById(id);

function tag(name, className, text) {
  const node = document.createElement(name);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function render() {
  el("status").replaceChildren();
  el("finding").replaceChildren();
  el("body").replaceChildren();
  el("tally").replaceChildren();
  el("doctitle").textContent = "";
  el("note").textContent = "";

  const res = current;
  if (!res) {
    el("strip").hidden = true;
    return;
  }
  if (res.error) el("status").append(ERRORS[res.error] || ERRORS.failed);

  // A finding is content, not a failure: it stands even on a page that gave up
  // no segments at all, which is exactly where it matters most.
  for (const f of res.hidden || []) el("finding").append(finding(f));

  const segments = res.segments || [];
  el("strip").hidden = segments.length === 0;
  if (!segments.length) {
    // A fetch can succeed and still yield nothing: without this the page just
    // goes blank, which reads as a broken demo rather than an answer.
    if (!res.error && !el("finding").hasChildNodes()) el("status").append(EMPTY);
    return;
  }

  el("tally").append(
    tally("paragraphs", segments.length),
    tally("cut", count(segments, "drop"), "n-cut"),
    tally("queried", count(segments, "flag"), "n-query"),
    tally("hidden", (res.hidden || []).length),
  );

  el("doctitle").textContent = res.title || "";
  el("note").textContent = note(segments);
  el("body").append(view === "human" ? sheet(segments) : agent(res.text || ""));
}

const count = (segments, verdict) => segments.filter((s) => s.verdict === verdict).length;

function tally(label, n, className) {
  const span = tag("span", className);
  span.append(`${n} `, tag("b", null, label));
  return span;
}

function finding(f) {
  const box = tag("div", "finding");
  box.append(tag("h2", null, `Hidden text found — ${f.kind}`));
  const p = tag("p", null, "The page carries text a reader never sees: ");
  p.append(tag("q", null, f.sample));
  box.append(p);
  return box;
}

function note(segments) {
  const cut = count(segments, "drop");
  const query = count(segments, "flag");
  const kept = count(segments, "keep");
  if (!cut && !query) {
    return "Nothing was cut. This page carries no declared advertising, and the filter does not invent any.";
  }
  const parts = [];
  if (cut) {
    parts.push(cut === 1
      ? "One paragraph the publisher marked as commercial was cut."
      : `${cut} paragraphs the publisher marked as commercial were cut.`);
  }
  if (query) {
    parts.push(query === 1
      ? "One doubtful paragraph stays in the text, marked, so an agent can weigh it."
      : `${query} doubtful paragraphs stay in the text, marked, so an agent can weigh them.`);
  }
  if (kept) parts.push(kept === 1 ? "One passed untouched." : `${kept} passed untouched.`);
  return parts.join(" ");
}

function sheet(segments) {
  const frag = document.createDocumentFragment();
  for (const s of segments) {
    const article = tag("article", `seg ${CLASS[s.verdict] || ""}`.trim());
    article.append(tag("p", "seg__text", s.text));
    if (s.verdict !== "keep") {
      const mark = tag("aside", "mark");
      mark.append(tag("span", "mark__verdict", LABEL[s.verdict]));
      mark.append(tag("span", "mark__reasons", (s.reasons || []).join(", ")));
      article.append(mark);
    }
    frag.append(article);
  }
  return frag;
}

// The markdown comes from the server's text field; only the markers around it
// are recognised here, and each piece still goes in as a text node.
function agent(text) {
  const pre = tag("pre", "agent");
  if (!text) {
    pre.textContent = "Everything on this page was advertising. The agent gets nothing.";
    return pre;
  }
  for (const piece of text.split(/(\[\[\/?adassay:flag.*?\]\])/)) {
    if (!piece) continue;
    pre.append(piece.startsWith("[[") ? tag("span", "marker", piece) : piece);
  }
  return pre;
}

function busy(on) {
  el("submit").disabled = on;
  for (const chip of chips) chip.disabled = on;
}

// busy() disables every entry point before the first await, so one request is
// in flight at a time and a slow answer cannot overwrite a later one.
async function load(request) {
  busy(true);
  current = null;
  render();
  el("status").append("Reading…");
  try {
    current = await (await request).json();
  } catch {
    current = { error: "failed" };
  }
  busy(false);
  render();
}

const openCase = (slug) => load(fetch(`/api/case/${slug}`));

const analyze = (url) =>
  load(fetch("/api/analyze", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ url }),
  }));

function press(buttons, pressed) {
  for (const b of buttons) b.setAttribute("aria-pressed", String(b === pressed));
}

const chips = document.querySelectorAll(".chip");
for (const chip of chips) {
  chip.onclick = () => {
    press(chips, chip);
    openCase(chip.dataset.case);
  };
}

const views = document.querySelectorAll(".switch button");
for (const button of views) {
  button.onclick = () => {
    press(views, button);
    view = button.dataset.view;
    render();
  };
}

el("form").onsubmit = (e) => {
  e.preventDefault();
  press(chips, null);
  analyze(el("url").value.trim());
};

openCase("keyboards");
