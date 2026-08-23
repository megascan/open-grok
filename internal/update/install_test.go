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
	t.Setenv("OPENGROK_HOME", t.TempDir())
	config.ResetHomeForTest()
	payload := []byte("open-grok-go-binary")
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	var assetURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})
	mux.HandleFunc("/bin.sha256", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(digest + "  open-grok\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	assetURL = srv.URL + "/bin"
	path, err := update.Install(update.Result{
		Current:  "test",
		Asset:    "open-grok",
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
		t.Fatalf("must install under OPENGROK_HOME/bin, got %s", path)
	}
}
