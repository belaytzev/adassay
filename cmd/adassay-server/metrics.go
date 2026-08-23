package main

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

// metrics is the whole observability surface of the server: a handful of
// counters plus two gauges read from the database at scrape time.
// ponytail: hand-written text exposition, client_golang when a histogram or dynamic labels appear
type metrics struct {
	bucketReqs atomic.Int64
	submitReqs atomic.Int64
	voteReqs   atomic.Int64
	bucketHits atomic.Int64
	divergent  atomic.Int64
}

// count wraps a handler in its request counter.
func (m *metrics) count(c *atomic.Int64, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.Add(1)
		h(w, r)
	}
}

// diverged is called from the store, which may run without metrics in tests.
func (m *metrics) diverged() {
	if m != nil {
		m.divergent.Add(1)
	}
}

func (m *metrics) handler(st *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		published, quarantined, err := st.stats()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "metrics unavailable")
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprintf(w, `# HELP adassay_requests_total Requests served, by endpoint.
# TYPE adassay_requests_total counter
adassay_requests_total{endpoint="bucket"} %d
adassay_requests_total{endpoint="submit"} %d
adassay_requests_total{endpoint="vote"} %d
# HELP adassay_bucket_hits_total Bucket lookups that held at least one real verdict.
# TYPE adassay_bucket_hits_total counter
adassay_bucket_hits_total %d
# HELP adassay_submit_divergent_total Submitted verdicts that disagreed with the stored one.
# TYPE adassay_submit_divergent_total counter
adassay_submit_divergent_total %d
# HELP adassay_verdicts_published Verdicts past quarantine and served to clients.
# TYPE adassay_verdicts_published gauge
adassay_verdicts_published %d
# HELP adassay_verdicts_quarantined Verdicts withheld until enough clients confirm them.
# TYPE adassay_verdicts_quarantined gauge
adassay_verdicts_quarantined %d
`,
			m.bucketReqs.Load(), m.submitReqs.Load(), m.voteReqs.Load(),
			m.bucketHits.Load(), m.divergent.Load(), published, quarantined)
	}
}
