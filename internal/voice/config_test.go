package voice

import (
	"strings"
	"testing"
)

func TestDefaultSTTWSURL(t *testing.T) {
	cfg := DefaultConfig()
	got, err := cfg.STTWSURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://api.x.ai/v1/stt" {
		t.Fatalf("%q", got)
	}
}

func TestSTTQueryParams(t *testing.T) {
	cfg := DefaultConfig()
	u, err := cfg.STTWSURLWithQuery()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sample_rate=16000", "encoding=pcm", "language=en"} {
		if !strings.Contains(u, want) {
			t.Fatalf("missing %q in %q", want, u)
		}
	}
	if strings.Contains(u, "language=auto") {
		t.Fatalf("must not send auto: %q", u)
	}
}

func TestAutoLanguageResolvedOnWire(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	t.Setenv("LANG", "C")
	cfg := DefaultConfig()
	cfg.Language = "auto"
	u, err := cfg.STTWSURLWithQuery()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(u, "language=auto") {
		t.Fatalf("auto leaked: %q", u)
	}
	if !strings.Contains(u, "language=") {
		t.Fatalf("missing language: %q", u)
	}
}

func TestHTTPAPIBaseRejected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIBase = "http://localhost:8080"
	if _, err := cfg.STTWSURL(); err == nil {
		t.Fatal("expected error")
	}
	cfg.APIBase = "ws://localhost:8080"
	if _, err := cfg.STTWSURL(); err == nil {
		t.Fatal("expected error")
	}
}

func TestV1BaseDedupesPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIBase = "https://proxy.example.com/v1"
	got, err := cfg.STTWSURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://proxy.example.com/v1/stt" {
		t.Fatalf("%q", got)
	}
}

func TestWSURLOverrideSkipsTLSCheck(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIBase = "http://localhost"
	cfg.WSURL = "ws://127.0.0.1:9/stt"
	got, err := cfg.STTWSURLWithQuery()
	if err != nil {
		t.Fatal(err)
	}
	if got != "ws://127.0.0.1:9/stt" {
		t.Fatalf("%q", got)
	}
}
