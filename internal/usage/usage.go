// Package usage reads what an agent session has consumed — tokens, how full
// its context window is, and what that would cost at API prices — from the
// agent's own transcript. Claude Code writes one JSONL transcript per
// session and names it in every hook payload (transcript_path).
//
// The cost is the API-equivalent price. On a subscription nobody is billed
// per token, but the figure still says which agent is expensive and roughly
// how much a task took.
package usage

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
)

// Price is dollars per million tokens.
type Price struct {
	Input, Output, CacheRead float64
	Window                   int // context window in tokens
}

// cacheWrite5m and cacheWrite1h are the standard multipliers on the input
// price for writing the prompt cache with a 5-minute or 1-hour TTL.
const (
	cacheWrite5m = 1.25
	cacheWrite1h = 2.0
)

// prices by model id prefix; longest match wins. Cache reads are 0.1x input
// except where a model's price sheet says otherwise.
var prices = map[string]Price{
	"claude-fable-5-1":  {10, 50, 0.25, 1_000_000},
	"claude-mythos-5-1": {10, 50, 0.25, 1_000_000},
	"claude-fable-5":    {10, 50, 1.00, 1_000_000},
	"claude-mythos-5":   {10, 50, 1.00, 1_000_000},
	"claude-opus-5-5":   {4, 20, 0.20, 1_000_000},
	"claude-opus-5":     {5, 25, 0.50, 1_000_000},
	"claude-opus-4":     {5, 25, 0.50, 1_000_000},
	"claude-sonnet-5":   {2, 10, 0.20, 1_000_000},
	"claude-sonnet-4":   {3, 15, 0.30, 1_000_000},
	"claude-haiku-4":    {1, 5, 0.10, 200_000},
}

// PriceFor finds the price for a model id such as "claude-opus-5" or
// "claude-sonnet-4-6[1m]". ok is false for an unknown model.
func PriceFor(model string) (Price, bool) {
	best, bestLen := Price{}, 0
	for prefix, p := range prices {
		if strings.HasPrefix(model, prefix) && len(prefix) > bestLen {
			best, bestLen = p, len(prefix)
		}
	}
	return best, bestLen > 0
}

// Usage is one session's consumption.
type Usage struct {
	Model        string  `json:"model"`
	Turns        int     `json:"turns"`
	Input        int     `json:"input_tokens"`
	Output       int     `json:"output_tokens"`
	CacheRead    int     `json:"cache_read_tokens"`
	CacheWrite   int     `json:"cache_write_tokens"`
	Context      int     `json:"context_tokens"` // prompt size of the latest turn: how full the window is
	Window       int     `json:"context_window"`
	ContextPct   int     `json:"context_pct"`
	Cost         float64 `json:"cost_usd"`
	KnownPricing bool    `json:"known_pricing"`
}

type line struct {
	Type    string `json:"type"`
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input         int `json:"input_tokens"`
			Output        int `json:"output_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			Breakdown     *struct {
				FiveMin int `json:"ephemeral_5m_input_tokens"`
				OneHour int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

type turn struct {
	model                         string
	input, output, read, w5m, w1h int
}

// Parse reads a Claude Code transcript. Claude Code writes a line per
// content block and repeats the message's usage on each, so turns are
// de-duplicated by message id, keeping the last (complete) usage.
func Parse(r io.Reader) Usage {
	turns := map[string]turn{}
	var order []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil || l.Type != "assistant" || l.Message.Usage == nil {
			continue
		}
		u := l.Message.Usage
		t := turn{model: l.Message.Model, input: u.Input, output: u.Output, read: u.CacheRead}
		if u.Breakdown != nil {
			t.w5m, t.w1h = u.Breakdown.FiveMin, u.Breakdown.OneHour
		} else {
			t.w5m = u.CacheCreation
		}
		id := l.Message.ID
		if id == "" {
			id = "#" + strconv.Itoa(len(order))
		}
		if _, seen := turns[id]; !seen {
			order = append(order, id)
		}
		turns[id] = t
	}

	var u Usage
	u.KnownPricing = true
	for _, id := range order {
		t := turns[id]
		if t.model == "<synthetic>" {
			continue
		}
		u.Turns++
		u.Model = t.model
		u.Input += t.input
		u.Output += t.output
		u.CacheRead += t.read
		u.CacheWrite += t.w5m + t.w1h
		u.Context = t.input + t.read + t.w5m + t.w1h
		p, ok := PriceFor(t.model)
		if !ok {
			u.KnownPricing = false
			continue
		}
		u.Cost += (float64(t.input)*p.Input +
			float64(t.output)*p.Output +
			float64(t.read)*p.CacheRead +
			float64(t.w5m)*p.Input*cacheWrite5m +
			float64(t.w1h)*p.Input*cacheWrite1h) / 1e6
	}
	if p, ok := PriceFor(u.Model); ok {
		u.Window = p.Window
		if p.Window > 0 {
			u.ContextPct = u.Context * 100 / p.Window
		}
	}
	return u
}

// File reads a transcript file.
func File(path string) (Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Usage{}, err
	}
	defer f.Close()
	return Parse(f), nil
}
