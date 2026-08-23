package version_test

import (
	"testing"

	"github.com/megascan/open-grok/internal/version"
)

func TestEmbeddedVersion(t *testing.T) {
	v := version.String()
	if v == "" || v == "0.0.0-dev" {
		t.Fatalf("embedded version %q", v)
	}
	if got := version.Display(); got == "" {
		t.Fatal("empty display")
	}
}
