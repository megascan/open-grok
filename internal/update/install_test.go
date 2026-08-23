package update_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/update"
)

func TestInstallVerifiesChecksum(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	payload := []byte("entropy-go-binary")
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	var assetURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})
	mux.HandleFunc("/bin.sha256", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(digest + "  entropy\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	assetURL = srv.URL + "/bin"
	path, err := update.Install(update.Result{
		Current:  "test",
		Asset:    "entropy",
		AssetURL: assetURL,
		GoOS:     runtime.GOOS,
		GoArch:   runtime.GOARCH,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("installed %q", got)
	}
	if !strings.HasPrefix(path, filepath.Join(config.Home(), "bin")) {
		t.Fatalf("must install under ENTROPY_HOME/bin, got %s", path)
	}
}

func TestAssetNameIsEntropy(t *testing.T) {
	if got := update.AssetName("linux", "amd64"); got != "entropy-linux-x86_64" {
		t.Fatalf("linux amd64 asset %q", got)
	}
	if got := update.AssetName("windows", "arm64"); got != "entropy-windows-aarch64.exe" {
		t.Fatalf("windows arm64 asset %q", got)
	}
}
