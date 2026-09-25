package usage

import (
	"math"
	"strings"
	"testing"
)

const transcript = `{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"assistant","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":1000,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":20000,"cache_creation":{"ephemeral_5m_input_tokens":20000,"ephemeral_1h_input_tokens":0}}}}
{"type":"assistant","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":0,"cache_creation_input_tokens":20000,"cache_creation":{"ephemeral_5m_input_tokens":20000,"ephemeral_1h_input_tokens":0}}}}
not json
{"type":"assistant","message":{"id":"msg_2","model":"claude-opus-5","usage":{"input_tokens":200,"output_tokens":300,"cache_read_input_tokens":21000,"cache_creation_input_tokens":0}}}
{"type":"assistant","message":{"id":"msg_3","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0}}}
`

func TestParseDeduplicatesAndPrices(t *testing.T) {
	u := Parse(strings.NewReader(transcript))
	if u.Turns != 2 {
		t.Fatalf("turns = %d, want 2 (msg_1 repeated per block, synthetic skipped)", u.Turns)
	}
	if u.Input != 1200 || u.Output != 800 || u.CacheRead != 21000 || u.CacheWrite != 20000 {
		t.Errorf("totals = %+v", u)
	}
	if u.Context != 21200 || u.Window != 1_000_000 || u.ContextPct != 2 {
		t.Errorf("context = %d/%d (%d%%)", u.Context, u.Window, u.ContextPct)
	}
	// opus 5: $5 in, $25 out, $0.50 read, 5m write = 1.25 x $5
	want := (1200*5.0 + 800*25.0 + 21000*0.5 + 20000*5*1.25) / 1e6
	if math.Abs(u.Cost-want) > 1e-9 || !u.KnownPricing {
		t.Errorf("cost = %f, want %f", u.Cost, want)
	}
}

func TestPriceForLongestPrefix(t *testing.T) {
	cases := map[string]float64{
		"claude-opus-5":         0.50,
		"claude-opus-5-5":       0.20,
		"claude-fable-5-1":      0.25,
		"claude-fable-5":        1.00,
		"claude-sonnet-4-6[1m]": 0.30,
	}
	for model, read := range cases {
		p, ok := PriceFor(model)
		if !ok || p.CacheRead != read {
			t.Errorf("%s: cache read %v (ok %v), want %v", model, p.CacheRead, ok, read)
		}
	}
	if p, _ := PriceFor("claude-haiku-4-5"); p.Window != 200_000 {
		t.Errorf("haiku window = %d", p.Window)
	}
	if _, ok := PriceFor("gpt-5"); ok {
		t.Error("unknown model priced")
	}
}

func TestUnknownModelFlagged(t *testing.T) {
	u := Parse(strings.NewReader(`{"type":"assistant","message":{"id":"a","model":"mystery-1","usage":{"input_tokens":5,"output_tokens":5}}}`))
	if u.KnownPricing || u.Cost != 0 || u.Turns != 1 {
		t.Errorf("unknown model: %+v", u)
	}
}

const codexRollout = `{"timestamp":"t","type":"session_meta","payload":{"id":"x","cwd":"/r"}}
{"timestamp":"t","type":"turn_context","payload":{"cwd":"/r","model":"gpt-5-codex"}}
{"timestamp":"t","type":"event_msg","payload":{"type":"token_count","info":null}}
{"timestamp":"t","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":5000,"cached_input_tokens":3000,"output_tokens":400,"reasoning_output_tokens":100,"total_tokens":5500},"last_token_usage":{"input_tokens":5000,"cached_input_tokens":3000,"output_tokens":400,"reasoning_output_tokens":100,"total_tokens":5500},"model_context_window":272000}}}
{"timestamp":"t","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":61000,"cached_input_tokens":50000,"output_tokens":900,"reasoning_output_tokens":300,"total_tokens":62200},"last_token_usage":{"input_tokens":56000,"cached_input_tokens":47000,"output_tokens":500,"reasoning_output_tokens":200,"total_tokens":136000},"model_context_window":272000}}}
`

func TestParseCodexRollout(t *testing.T) {
	u := Parse(strings.NewReader(codexRollout))
	if u.Model != "gpt-5-codex" || u.Turns != 2 {
		t.Fatalf("model/turns = %q/%d", u.Model, u.Turns)
	}
	if u.Input != 61000 || u.CacheRead != 50000 || u.Output != 1200 {
		t.Errorf("totals = %+v", u)
	}
	if u.Context != 136000 || u.Window != 272000 || u.ContextPct != 50 {
		t.Errorf("context = %d/%d (%d%%)", u.Context, u.Window, u.ContextPct)
	}
	if u.KnownPricing || u.Cost != 0 {
		t.Error("Codex cost must stay unknown, not guessed")
	}
}
