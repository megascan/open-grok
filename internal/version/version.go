// Package version reports the Entropy product identity for the Go native binary.
package version

import (
	_ "embed"
	"os"
	"runtime/debug"
	"strings"
	"sync"
)

//go:embed VERSION
var embedded string

const (
	fallback = "0.0.0-dev"
	// Product is the user-facing name of the shipping Go application.
	Product = "Entropy"
	// CLIName is the public command (`entropy`).
	CLIName = "entropy"
)

var (
	// verOverride is filled by -ldflags at release build time.
	verOverride string
	once        sync.Once
	ver         string
)

// String returns the product version from ENTROPY_VERSION, OPEN_GROK_VERSION,
// the embedded VERSION file, or the Go module VCS revision.
func String() string {
	once.Do(func() {
		ver = resolve()
	})
	return ver
}

func resolve() string {
	if env := strings.TrimSpace(os.Getenv("ENTROPY_VERSION")); env != "" {
		return env
	}
	if env := strings.TrimSpace(os.Getenv("OPEN_GROK_VERSION")); env != "" {
		return env
	}
	if v := strings.TrimSpace(verOverride); v != "" {
		return v
	}
	if v := strings.TrimSpace(embedded); v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return fallback + "+" + s.Value[:7]
			}
		}
	}
	return fallback
}

// Display returns the user-facing version line printed by --version.
func Display() string {
	return CLIName + " " + String() + " (go)"
}
