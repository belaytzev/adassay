# Extraction fixtures

Small HTML files the extractor is tested against. Unit tests must run offline, so unlike the
calibration corpus these ship with the repository.

**Synthetic** — everything except the four files below. Each isolates one mechanism:
`display:none`, off-screen positioning, a `noscript` block, zero-width characters, a legitimate
`sr-only` label that must *not* be reported. Written for this project, MIT like the code.

**Captured** — `real_go_dev.html`, `real_mdn.html`, `real_wikipedia.html`, `thirstybear.html`.
Excerpts of pages from go.dev, MDN, Wikipedia and thirstybear.com, kept because a
hidden-text detector tuned only on synthetic examples measures nothing: the first three
establish how much noise real markup produces on honest pages, and the fourth is the case
where an extractor deletes affiliate markup before the rules can see it.

They are stripped of scripts, styles and inline SVG, and are excerpts rather than complete
articles. Copyright remains with their publishers; they are here to test software, not to
republish anything. Replace them with synthetic equivalents if that distinction ever stops
being comfortable.
