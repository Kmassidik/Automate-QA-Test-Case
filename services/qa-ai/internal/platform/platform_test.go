package platform

import "testing"

func TestLabel(t *testing.T) {
	cases := []struct {
		in   Info
		want string
	}{
		{Info{OS: "darwin"}, "macOS (native)"},
		{Info{OS: "linux"}, "Linux (native)"},
		{Info{OS: "windows"}, "Windows (native)"},
		{Info{OS: "linux", InContainer: true}, "Linux (container)"},
	}
	for _, c := range cases {
		if got := c.in.Label(); got != c.want {
			t.Errorf("Label(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsMacOnlyNative(t *testing.T) {
	if !(Info{OS: "darwin"}).IsMac() {
		t.Error("native darwin should be Mac")
	}
	// A Linux container on a Mac host reports linux — must not be treated as Mac.
	if (Info{OS: "linux", InContainer: true}).IsMac() {
		t.Error("linux container must not be Mac")
	}
	if (Info{OS: "darwin", InContainer: true}).IsMac() {
		t.Error("containerized darwin should not claim native Mac guidance")
	}
}

func TestGuidanceIsOSSpecific(t *testing.T) {
	mac := Info{OS: "darwin"}.OllamaGuidance("http://localhost:11434")
	if !contains(mac, "Metal") || !contains(mac, "NATIVELY") {
		t.Errorf("mac guidance should mention native/Metal: %q", mac)
	}
	linux := Info{OS: "linux"}.OllamaGuidance("http://localhost:11434")
	if !contains(linux, "Docker") {
		t.Errorf("linux guidance should mention Docker option: %q", linux)
	}
	ctr := Info{OS: "linux", InContainer: true}.OllamaGuidance("http://qa-ai:11434")
	if !contains(ctr, "host") {
		t.Errorf("container guidance should mention host: %q", ctr)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
