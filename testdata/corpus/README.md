# Calibration corpus

Pages used to measure whether a change to the detector helped. `labels.yaml` records, per
page, which paragraphs are advertising and whether the page carries text hidden from readers.

## Two kinds of file, with different status

**Synthetic fixtures** — `promo_*`, `inj_*`, `ctl_*` — are written for this project and carry
the same MIT licence as the code. They exercise one mechanism each: a disclosure line, a promo
code, an injection in a `meta` attribute, a control page that mentions advertising without
being advertising.

**Captured pages** — `ad_*`, `clean_*` — are copies of third-party articles, kept because a
detector tuned only on synthetic examples measures nothing. **They are not ours to license.**
Copyright stays with their publishers. They are here for calibration and research, not for
redistribution, and `labels.yaml` records the source URL of each.

## Before publishing this repository

Shipping 50 complete copies of commercial articles is a copyright problem, and it is sharper
here than usual: this project filters the advertising those publishers sell.

Options, in order of preference:

1. **Ship URLs, not pages.** Keep `labels.yaml` with its source URLs plus a fetch script, and
   let a contributor download the pages themselves. This is how ML datasets normally handle
   the same problem. Corpus-dependent tests skip when the pages are absent.
2. **Trim to fragments.** Keep only the paragraphs the labels reference. Weakens extraction
   tests, since those need the surrounding document structure.
3. **Ask permission.** Thorough, slow, and unlikely to scale past a handful of publishers.

Whichever is chosen, do it before the first public push: once the pages are in a public
history they are in every clone and fork.
