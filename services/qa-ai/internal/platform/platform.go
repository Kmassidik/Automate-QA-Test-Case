// Package platform detects where qa-ai is running (OS + container) so the
// Ollama preflight can give correct, actionable guidance. The macOS "Ollama
// must be native" rule (no Metal GPU inside Docker's Linux VM) does NOT apply
// on Linux, where Ollama may run natively or in a container — so the advice has
// to be OS-aware instead of hard-coded for Mac.
package platform

import (
	"os"
	"runtime"
)

type Info struct {
	OS          string // runtime.GOOS: "darwin", "linux", "windows", …
	InContainer bool
}

// Detect inspects the runtime. Container detection uses /.dockerenv (present in
// Docker images) with an env override for other runtimes (Colima/Podman/k8s).
func Detect() Info {
	return Info{
		OS:          runtime.GOOS,
		InContainer: inContainer(),
	}
}

func inContainer() bool {
	if v := os.Getenv("QA_IN_CONTAINER"); v == "1" || v == "true" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

// IsMac reports whether the host is macOS (only meaningful when NOT containerized,
// since a Linux container reports "linux" even on a Mac host).
func (i Info) IsMac() bool { return i.OS == "darwin" && !i.InContainer }

// Label is a short human description, e.g. "macOS (native)" or "linux (container)".
func (i Info) Label() string {
	name := i.OS
	switch i.OS {
	case "darwin":
		name = "macOS"
	case "linux":
		name = "Linux"
	case "windows":
		name = "Windows"
	}
	if i.InContainer {
		return name + " (container)"
	}
	return name + " (native)"
}

// OllamaGuidance returns a one-paragraph, OS-aware hint for making Ollama
// reachable at ollamaURL. It's shown in logs (preflight) and surfaced via
// /healthz so the operator sees exactly what to do.
func (i Info) OllamaGuidance(ollamaURL string) string {
	if i.InContainer {
		// Inside a container we reach Ollama on the host; the right fix depends on
		// the host, which we can't see — so cover both common cases.
		return "qa-ai is running in a container and expects Ollama on the host at " + ollamaURL + ". " +
			"On macOS/Windows Docker Desktop, host.docker.internal resolves automatically. " +
			"On Linux, ensure compose maps it (extra_hosts: \"host.docker.internal:host-gateway\"), " +
			"or run Ollama as a container on the same network (see compose.linux.yml)."
	}
	switch i.OS {
	case "darwin":
		return "macOS detected. Ollama cannot use the GPU inside Docker here (no Metal in the Linux VM), " +
			"so run it NATIVELY on the host: `ollama serve` (the model is pulled automatically once it's up)."
	case "linux":
		return "Linux detected. Start Ollama with `ollama serve` (native), or run it in Docker " +
			"(`docker run -d -p 11434:11434 ollama/ollama`). For an NVIDIA GPU, install the container toolkit. " +
			"The model is pulled automatically once Ollama is reachable."
	case "windows":
		return "Windows detected. Run Ollama natively (or under WSL2): `ollama serve`. " +
			"The model is pulled automatically once it's up."
	default:
		return "Start Ollama and make it reachable at " + ollamaURL + "; the model is pulled automatically once it's up."
	}
}
