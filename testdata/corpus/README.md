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

## Getting the pages

```sh
./fetch.sh          # download what is missing
./fetch.sh --force  # re-download everything
```

Captured pages are not in the repository — `.gitignore` keeps them out and this script pulls
them from the URLs in `labels.yaml`. Without them, calibration still runs on the synthetic
fixtures and says how many pages are missing; the corpus tests skip rather than fail.

**Live pages drift, and the corpus notices.** A label that no longer matches its page is
reported rather than fatal — the page moved on, and the label needs revisiting. For synthetic
fixtures the same mismatch is still an error, because those files cannot change on their own.
Price labels drift fastest, since a roundup reprices weekly.

The same applies to the hidden-text baseline: a redesign can add legitimately hidden markup —
python.org hides the labels of its theme switcher — and the noise floor in
`maxHiddenFalsePositives` moves with it.

## Why they are not committed

Shipping 50 complete copies of commercial articles would be a copyright problem, and a sharper
one than usual: this project filters the advertising those publishers sell. So the repository
carries the labels and the URLs, and each contributor fetches the pages themselves — the same
arrangement ML datasets use for the same reason.

The trade is that the corpus is no longer byte-identical for everyone. A page can change or
disappear between one person's run and another's, which is why drift is reported instead of
being fatal. Reproducibility of the *metrics* comes from the labels, not from the bytes.
