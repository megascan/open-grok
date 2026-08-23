// Package update checks GitHub Releases for a newer Open Grok Go binary.
package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/version"
)

const defaultFeed = "https://api.github.com/repos/megascan/open-grok/releases/latest"

// Result is a --check payload.
type Result struct {
	Current  string `json:"current"`
	Latest   string `json:"latest"`
	Newer    bool   `json:"newer"`
	Asset    string `json:"asset,omitempty"`
	GoOS     string `json:"goos"`
	GoArch   string `json:"goarch"`
	AssetURL string `json:"asset_url,omitempty"`
}

// Check queries the public release feed. It never overwrites the running binary.
func Check() (Result, error) {
	r := Result{Current: version.String(), GoOS: runtime.GOOS, GoArch: runtime.GOARCH}
	req, err := http.NewRequest(http.MethodGet, defaultFeed, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("User-Agent", "open-grok/"+version.String())
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return r, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return r, fmt.Errorf("release feed HTTP %d", resp.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(data, &body); err != nil {
		return r, err
	}
	r.Latest = strings.TrimPrefix(body.TagName, "v")
	r.Newer = r.Latest != "" && r.Latest != r.Current
	want := assetName(runtime.GOOS, runtime.GOARCH)
	r.Asset = want
	for _, a := range body.Assets {
		if a.Name == want {
			r.AssetURL = a.URL
			break
		}
	}
	return r, nil
}

// AssetName is the published filename for a GOOS/GOARCH pair.
func assetName(goos, goarch string) string {
	arch := goarch
	switch goarch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	name := "open-grok-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}
