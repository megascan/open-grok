package version_test

import (
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/version"
)

func TestEmbeddedVersion(t *testing.T) {
	v := version.String()
	if v == "" || v == "0.0.0-dev" {
		t.Fatalf("embedded version %q", v)
	}
	if got := version.Display(); !strings.Contains(got, "entropy") {
		t.Fatalf("display %q must name entropy", got)
	}
	if version.Product != "Entropy" || version.CLIName != "entropy" {
		t.Fatalf("product %q cli %q", version.Product, version.CLIName)
	}
}
