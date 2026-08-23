package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/config"
)

// Install downloads the matching GitHub release asset into
// $ENTROPY_HOME/bin/entropy after verifying SHA-256 when a sidecar exists.
// It never writes to ~/.grok.
func Install(res Result) (string, error) {
	if res.AssetURL == "" {
		return "", fmt.Errorf("no GitHub asset for %s/%s (%s)", res.GoOS, res.GoArch, res.Asset)
	}
	dest := config.Application()
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), "entropy-download-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	client := &http.Client{Timeout: 3 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, res.AssetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "entropy/"+res.Current)
	resp, err := client.Do(req)
	if err != nil {
		_ = tmp.Close()
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_ = tmp.Close()
		return "", fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(resp.Body, 256<<20)); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(sum.Sum(nil))
	if expect, ok := fetchChecksum(client, res.AssetURL+".sha256"); ok && !strings.EqualFold(expect, digest) {
		return "", fmt.Errorf("sha256 mismatch: got %s want %s", digest, expect)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		data, rerr := os.ReadFile(tmpName)
		if rerr != nil {
			return "", err
		}
		if werr := os.WriteFile(dest, data, 0o755); werr != nil {
			return "", werr
		}
	}
	return dest, nil
}

func fetchChecksum(client *http.Client, url string) (string, bool) {
	resp, err := client.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return "", false
	}
	return fields[0], true
}
