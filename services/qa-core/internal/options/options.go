// Package options holds the user-editable form vocabularies (review.md #1): the
// dropdown/checkbox choices a QA lead can manage from the Settings page without
// touching code. These are free-form strings the LLM reads as-is. Output format
// is intentionally NOT here — it's coupled to the renderer/exports, so it stays
// fixed in code. Persisted as a small JSON file (no DB, per the PRD).
package options

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Options is the editable vocabulary, split into two axes (review.md #3/#7):
// CaseNatures (the nature of a case) and TestDimensions (functional vs the
// non-functional concerns), plus the application types.
type Options struct {
	ApplicationTypes []string `json:"application_types"`
	CaseNatures      []string `json:"case_natures"`
	TestDimensions   []string `json:"test_dimensions"`
}

func defaults() Options {
	return Options{
		ApplicationTypes: []string{"Web", "Mobile", "Desktop"},
		CaseNatures:      []string{"Positive", "Negative", "Edge case"},
		TestDimensions:   []string{"Functional", "Security", "Performance", "Accessibility", "Usability"},
	}
}

type Store struct {
	mu   sync.RWMutex
	path string // "" => in-memory only (edits not persisted across restarts)
	cur  Options
}

// Load reads the options file if present, else seeds defaults. A missing or
// unreadable file is not an error — we fall back to defaults.
func Load(path string) *Store {
	s := &Store{path: path, cur: defaults()}
	if path == "" {
		return s
	}
	if b, err := os.ReadFile(path); err == nil {
		var o Options
		if json.Unmarshal(b, &o) == nil {
			s.cur = sanitize(o)
		}
	}
	return s
}

func (s *Store) Get() Options {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Set validates, stores, and persists (best-effort) the new options. Returns an
// error only if the file write fails; the in-memory value is always updated.
func (s *Store) Set(o Options) error {
	o = sanitize(o)
	s.mu.Lock()
	s.cur = o
	path := s.path
	s.mu.Unlock()
	if path == "" {
		return nil
	}
	if d := filepath.Dir(path); d != "" {
		_ = os.MkdirAll(d, 0o755)
	}
	b, _ := json.MarshalIndent(o, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

// sanitize trims, de-dups, drops blanks, and restores defaults for any list left
// empty — a Settings save that wiped a whole list shouldn't brick the form.
func sanitize(o Options) Options {
	d := defaults()
	o.ApplicationTypes = orDefault(clean(o.ApplicationTypes), d.ApplicationTypes)
	o.CaseNatures = orDefault(clean(o.CaseNatures), d.CaseNatures)
	o.TestDimensions = orDefault(clean(o.TestDimensions), d.TestDimensions)
	return o
}

func clean(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[strings.ToLower(v)] {
			continue
		}
		seen[strings.ToLower(v)] = true
		out = append(out, v)
	}
	return out
}

func orDefault(v, def []string) []string {
	if len(v) == 0 {
		return def
	}
	return v
}
