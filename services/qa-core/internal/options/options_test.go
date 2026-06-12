package options

import (
	"path/filepath"
	"testing"
)

func TestSanitizeDedupAndDefaults(t *testing.T) {
	s := Load("")
	_ = s.Set(Options{
		ApplicationTypes: []string{" Web ", "Web", "API", ""}, // trim + dedup + drop blank
		CaseNatures:      nil,                                 // empty -> defaults
		TestDimensions:   []string{"Security"},
	})
	o := s.Get()
	if len(o.ApplicationTypes) != 2 || o.ApplicationTypes[0] != "Web" || o.ApplicationTypes[1] != "API" {
		t.Errorf("app types not cleaned: %v", o.ApplicationTypes)
	}
	if len(o.CaseNatures) != 4 { // restored to defaults
		t.Errorf("empty case natures should fall back to defaults, got %v", o.CaseNatures)
	}
	if len(o.TestDimensions) != 1 || o.TestDimensions[0] != "Security" {
		t.Errorf("dimensions = %v", o.TestDimensions)
	}
}

func TestPersistAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "options.json")
	s := Load(path)
	if err := s.Set(Options{ApplicationTypes: []string{"Web", "CLI"}}); err != nil {
		t.Fatal(err)
	}
	// A fresh Load from the same path should see the persisted values.
	s2 := Load(path)
	o := s2.Get()
	if len(o.ApplicationTypes) != 2 || o.ApplicationTypes[1] != "CLI" {
		t.Errorf("reload lost data: %v", o.ApplicationTypes)
	}
}
