package memory

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// stopwords never help find a memory.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be but by for from has have how i if in into is it its
		of on or that the their then there these this to was were what when where which while why will with
		you your we our not no do does did can could should would just so than too very about after before`) {
		stopwords[w] = true
	}
}

// Tokens splits text into search terms. Identifiers are split as well as kept
// whole — `fitBox` yields fitbox, fit and box; `max_height` yields
// max_height, max and height — because a memory about fitBox should be found
// by someone searching "fit box" and by someone searching "fitBox".
func Tokens(text string) []string {
	var out []string
	add := func(w string) {
		w = strings.ToLower(w)
		if len(w) < 2 || stopwords[w] {
			return
		}
		out = append(out, stem(w))
	}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		add(word)
		parts := splitIdent(word)
		if len(parts) > 1 {
			for _, p := range parts {
				add(p)
			}
		}
	}
	return out
}

// splitIdent breaks camelCase, PascalCase and snake_case into words.
func splitIdent(w string) []string {
	var parts []string
	var cur []rune
	runes := []rune(w)
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, string(cur))
			cur = cur[:0]
		}
	}
	for i, r := range runes {
		switch {
		case r == '_':
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) ||
			(i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1]))):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return parts
}

// stem is deliberately light: plurals and common verb endings, enough that
// "tests"/"testing"/"tested" meet "test" without mangling identifiers.
func stem(w string) string {
	for _, suf := range []string{"ing", "ed", "es", "s"} {
		if len(w) > len(suf)+3 && strings.HasSuffix(w, suf) {
			return strings.TrimSuffix(w, suf)
		}
	}
	return w
}

// Field weights: a word in the title says more about a memory than the same
// word deep in its body.
const (
	weightTitle = 5
	weightTags  = 3
	weightPaths = 2
	weightBody  = 1
)

func weighted(m Memory) map[string]float64 {
	tf := map[string]float64{}
	add := func(text string, w float64) {
		for _, t := range Tokens(text) {
			tf[t] += w
		}
	}
	title := m.Title()
	add(title, weightTitle)
	add(strings.Join(m.Tags, " "), weightTags)
	add(strings.Join(m.Paths, " "), weightPaths)
	add(strings.TrimPrefix(strings.TrimSpace(m.Body), title), weightBody)
	add(m.Kind, weightBody)
	return tf
}

// Hit is a ranked search result.
type Hit struct {
	Memory Memory
	Score  float64
}

// Options narrow a search.
type Options struct {
	Kind  string
	Scope string   // "project", "global" or "" for both
	Paths []string // the caller's files: memories about them rank higher
	Limit int
	Now   time.Time
}

// Search ranks memories against query with BM25 over weighted fields, then
// adjusts for scope, recency, use and path overlap.
func Search(mems []Memory, query string, o Options) []Hit {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	var pool []Memory
	for _, m := range mems {
		if (o.Kind == "" || m.Kind == o.Kind) && (o.Scope == "" || m.Scope == o.Scope) {
			pool = append(pool, m)
		}
	}
	terms := unique(Tokens(query))
	if len(pool) == 0 || len(terms) == 0 {
		return nil
	}

	docs := make([]map[string]float64, len(pool))
	lengths := make([]float64, len(pool))
	df := map[string]int{}
	var total float64
	for i, m := range pool {
		docs[i] = weighted(m)
		for t, w := range docs[i] {
			lengths[i] += w
			df[t]++
		}
		total += lengths[i]
	}
	avg := total / float64(len(pool))
	const k1, b = 1.2, 0.75
	n := float64(len(pool))

	var hits []Hit
	for i, m := range pool {
		var score float64
		for _, t := range terms {
			tf := docs[i][t]
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
			score += idf * tf * (k1 + 1) / (tf + k1*(1-b+b*lengths[i]/avg))
		}
		if score == 0 {
			continue
		}
		score *= boost(m, o)
		hits = append(hits, Hit{Memory: m, Score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if o.Limit > 0 && len(hits) > o.Limit {
		hits = hits[:o.Limit]
	}
	return hits
}

// boost multiplies a BM25 score by what makes a memory more likely to be the
// one wanted: this project's, recently used, often used, about the files in
// hand.
func boost(m Memory, o Options) float64 {
	f := 1.0
	if m.Scope != "global" {
		f *= 1.2
	}
	last := m.LastUsed
	if last.IsZero() {
		last = m.Updated
	}
	if !last.IsZero() {
		days := o.Now.Sub(last).Hours() / 24
		f *= 0.6 + 0.4*math.Exp(-days/90)
	}
	f *= 1 + 0.1*math.Log1p(float64(m.Uses))
	if overlaps(m.Paths, o.Paths) {
		f *= 1.5
	}
	if m.Pinned {
		f *= 1.3
	}
	return f
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y || strings.HasSuffix(y, "/"+x) || strings.HasSuffix(x, "/"+y) {
				return true
			}
		}
	}
	return false
}

func unique(ts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range ts {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// Similar returns an existing memory that says much the same as text: one
// whose terms cover at least 60% of the new text's distinct terms. It is how
// `remember` avoids piling up the same lesson written five ways.
func Similar(mems []Memory, text string) (Memory, bool) {
	want := unique(Tokens(text))
	if len(want) < 3 {
		return Memory{}, false
	}
	best, bestCover := Memory{}, 0.0
	for _, m := range mems {
		have := weighted(m)
		n := 0
		for _, t := range want {
			if have[t] > 0 {
				n++
			}
		}
		if cover := float64(n) / float64(len(want)); cover > bestCover {
			best, bestCover = m, cover
		}
	}
	return best, bestCover >= 0.6
}
