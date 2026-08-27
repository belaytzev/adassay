package judge

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
)

// Runs against a real gateway, so it stays off by default: it costs money and
// it fails when the network does. Point the three variables at one to check
// that the model still honours the schema and still reads the segments the way
// the rules do.
//
//	ADASSAY_JUDGE_URL=http://127.0.0.1:14000 \
//	ADASSAY_JUDGE_MODEL=gpt-5.6-luna \
//	ADASSAY_JUDGE_KEY=sk-... go test ./internal/judge -run Gateway -v
func TestGatewayAnswersTheRealPrompt(t *testing.T) {
	url, model, key := os.Getenv(config.EnvJudgeURL), os.Getenv(config.EnvJudgeModel), os.Getenv(config.EnvJudgeKey)
	if url == "" || model == "" || key == "" {
		t.Skipf("set %s, %s and %s to run this", config.EnvJudgeURL, config.EnvJudgeModel, config.EnvJudgeKey)
	}

	j := New(config.Judge{Endpoint: url, Model: model, Timeout: 120 * time.Second, BatchSize: 8, APIKey: key})
	j.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))

	got := j.Decide("a review of kitchen blenders", []core.Segment{
		{ID: "s1", Text: "Readers of this site get 20% off with code BLEND20 at checkout — our thanks to our partner.", Verdict: core.Flag},
		{ID: "s2", Text: "A blade grinder heats the beans unevenly, which is why the grind tastes flat.", Verdict: core.Flag},
	})

	// core.Keep is the zero value, so a missing key reads as a keep. Absence is
	// a failed call, not a disagreement, and the two need different fixes.
	v1, ok := got["s1"]
	if !ok {
		t.Fatal("the judge returned no verdict for s1: the call failed rather than the model " +
			"disagreeing — the warning above says why")
	}
	if v1 != core.Drop {
		t.Errorf("s1 = %v, want drop: a discount code offered on the publisher's behalf is the "+
			"clearest case there is", v1)
	}
	if got["s2"] == core.Drop {
		t.Errorf("s2 = %v, want anything but drop: dropping plain editorial is the failure that "+
			"costs a reader the article", got["s2"])
	}
}
