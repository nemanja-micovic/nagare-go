package demo

import (
	"fmt"
	"strings"
	"time"
)

// scenario is one agent's opening task.
type scenario struct {
	delay time.Duration // pause after the banner, so agents do not move in lockstep
	task  string
	steps []step
}

const sec = time.Second

// scenarios are timed so a demo shows every state nagare cares about within its
// first minute: one agent blocks on a permission prompt early, one finishes, one
// keeps grinding, and a second prompt arrives later — which is what makes the
// waiting queue and F4 worth showing.
var scenarios = map[string]scenario{
	"retry": {
		delay: 1 * sec,
		task:  "Make token refresh retry with exponential backoff and jitter",
		steps: []step{
			{think: 3 * sec, say: "I'll look at how refresh is wired today."},
			{tool: "Read(internal/auth/refresh.go)", result: "Read 48 lines"},
			{think: 3 * sec, say: "Refresh gives up after one failure. I'll wrap it in a bounded retry loop with jittered backoff."},
			{edit: &edit{path: "internal/auth/refresh.go", lines: []string{
				"func refreshWithRetry(ctx context.Context) error {",
				"\tdelay := 200 * time.Millisecond",
				"\tfor attempt := 0; attempt < maxRetries; attempt++ {",
				"\t\tif err := refresh(ctx); err == nil {",
				"\t\t\treturn nil",
				"\t\t}",
				"\t\ttime.Sleep(delay + jitter(delay))",
				"\t\tdelay *= 2",
				"\t}",
				"\treturn ErrRefreshExhausted",
				"}",
			}}},
			{think: 2 * sec, tool: "Bash(go test ./internal/auth/...)", ask: "go test ./internal/auth/...",
				result: "ok  \tgithub.com/acme/api/internal/auth\t0.412s"},
			{think: 2 * sec, say: "Refresh now retries up to 5 times with jittered backoff (200ms → 3.2s). All auth tests pass."},
		},
	},
	"ratelimit": {
		delay: 2 * sec,
		task:  "Add a per-client token-bucket rate limiter to the HTTP middleware",
		steps: []step{
			{think: 4 * sec, say: "Planning: a token bucket per API key, kept in a sharded map with idle eviction."},
			{tool: "Read(internal/http/middleware.go)", result: "Read 112 lines"},
			{think: 6 * sec},
			{edit: &edit{path: "internal/http/ratelimit.go", lines: []string{
				"type bucket struct {",
				"\ttokens   float64",
				"\tlastFill time.Time",
				"}",
				"",
				"func (b *bucket) take(rate, burst float64, now time.Time) bool {",
				"\tb.tokens = min(burst, b.tokens+rate*now.Sub(b.lastFill).Seconds())",
				"\tb.lastFill = now",
				"\tif b.tokens < 1 {",
				"\t\treturn false",
				"\t}",
				"\tb.tokens--",
				"\treturn true",
				"}",
			}}},
			{think: 8 * sec, say: "Wiring the limiter into the middleware chain, before auth so rejected clients stay cheap."},
			{edit: &edit{path: "internal/http/middleware.go", lines: []string{
				"\th = RateLimit(h, cfg.Rate, cfg.Burst)",
			}}},
			{think: 10 * sec, tool: "Bash(go test ./internal/http/...)", result: "ok  \tgithub.com/acme/api/internal/http\t1.208s"},
			{think: 6 * sec, say: "Added a per-key token bucket (10 req/s, burst 20) with 429 + Retry-After. Tests and a 10k-request load check pass."},
		},
	},
	"hero": {
		delay: 3 * sec,
		task:  "Build the landing page hero with the new gradient headline",
		steps: []step{
			{think: 5 * sec, say: "I'll start from the existing layout and add a Hero component."},
			{tool: "Read(src/pages/index.tsx)", result: "Read 64 lines"},
			{think: 5 * sec},
			{edit: &edit{path: "src/components/Hero.tsx", lines: []string{
				"export function Hero() {",
				"  return (",
				"    <section className=\"hero\">",
				"      <h1 className=\"gradient\">Ship with your agents</h1>",
				"      <p>Every session, one screen.</p>",
				"    </section>",
				"  )",
				"}",
			}}},
			{think: 12 * sec, say: "The component is in. I need to install the animation library it uses."},
			{think: 2 * sec, tool: "Bash(npm install motion)", ask: "npm install motion",
				result: "added 3 packages in 2.1s"},
			{think: 4 * sec, say: "Hero section is done: gradient headline, subtitle, and a fade-in on load."},
		},
	},
	"docs": {
		delay: 4 * sec,
		task:  "Write the getting-started guide for the CLI",
		steps: []step{
			{think: 6 * sec, say: "Drafting an outline: install, first run, configuration, troubleshooting."},
			{edit: &edit{path: "docs/getting-started.md", lines: []string{
				"# Getting started",
				"",
				"Install the CLI, then run `acme init` in your project.",
			}}},
			{think: 7 * sec},
			{edit: &edit{path: "docs/getting-started.md", lines: []string{
				"",
				"## Configuration",
				"",
				"Settings live in `acme.toml` at the repository root.",
			}}},
			{think: 5 * sec, say: "The guide covers install, first run and configuration. Want a troubleshooting section too?"},
		},
	},
}

// followUp is the script for anything typed into an idle demo agent, so the
// demo answers whatever the viewer tries.
func followUp(text string) []step {
	short := text
	if len(short) > 60 {
		short = short[:57] + "..."
	}
	slug := slugify(short)
	return []step{
		{think: 3 * sec, say: fmt.Sprintf("On it: %s", short)},
		{edit: &edit{path: "notes/" + slug + ".md", lines: []string{
			"# " + short,
			"",
			"- [x] understood the request",
			"- [x] made the change",
		}}},
		{think: 3 * sec, say: "Done. That was a simulated agent; in real use this is Claude Code, Codex, or any other agent."},
	}
}

// slugify turns free text into a short file name: lowercase words joined by
// single dashes, cut at a word boundary.
func slugify(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	slug := ""
	for _, w := range words {
		if slug != "" && len(slug)+1+len(w) > 32 {
			break
		}
		if slug != "" {
			slug += "-"
		}
		slug += w
	}
	if slug == "" {
		return "note"
	}
	return slug
}
