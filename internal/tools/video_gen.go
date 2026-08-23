package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/types"
)

const (
	xaiVideoBaseModel    = "grok-imagine-video"
	xaiVideoQualityModel = "grok-imagine-video-1.5-preview"
	videoStartTimeout    = 60 * time.Second
	videoGenTimeout      = 300 * time.Second
	videoPollReqTimeout  = 30 * time.Second
	videoDownloadTimeout = 120 * time.Second
	defaultVideoDuration = 6
	defaultVideoRes      = "480p"
	maxRefImages         = 7
	videoResultTmpl      = "Video generated and saved to %s. Do not read or re-display it, and do not describe how it appears to the user."
)

var (
	validVideoDurations = []int{6, 10}
	validVideoRes       = []string{"480p", "720p"}
	validVideoAspects   = []string{"1:1", "16:9", "9:16", "3:2", "2:3"}
)

// videoClient is the shared xAI Video Generation helper used by
// image_to_video and reference_to_video.
//
// PollInterval of 0 skips the production 5s wait so httptest can drive
// POST+poll+download without sleeping. Production wiring should set
// PollInterval: 5 * time.Second (see glue).
type videoClient struct {
	HTTP         *http.Client
	BaseURL      string
	Bearer       string
	Model        string
	PollInterval time.Duration
}

func (c videoClient) resolveRoute() (base, bearer, model string, err error) {
	model = c.Model
	base = strings.TrimSpace(c.BaseURL)
	if base == "" {
		base = catalog.DefaultBaseURL(types.ProviderXAI)
	}
	bearer = strings.TrimSpace(c.Bearer)
	if bearer == "" {
		token, _, e := auth.ResolveBearer(types.ProviderXAI, auth.EnvKeys(types.ProviderXAI), false)
		if e != nil || token == "" {
			return "", "", "", fmt.Errorf("not signed in to xAI (try `entropy login xai`)")
		}
		bearer = token
	}
	return base, bearer, model, nil
}

func (c videoClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: videoGenTimeout}
}

func (c videoClient) generate(ctx context.Context, tc Context, prompt, resolution string, duration int, aspect *string, imageURL string, refURLs []string) Result {
	base, bearer, model, err := c.resolveRoute()
	if err != nil {
		return errResult(err.Error())
	}
	if model == "" {
		model = xaiVideoQualityModel
	}
	payload := map[string]any{
		"model":      model,
		"prompt":     prompt,
		"duration":   duration,
		"resolution": resolution,
	}
	if imageURL != "" {
		payload["image"] = map[string]any{"url": imageURL}
	}
	if aspect != nil && *aspect != "" {
		payload["aspect_ratio"] = *aspect
	}
	if len(refURLs) > 0 {
		refs := make([]map[string]any, len(refURLs))
		for i, u := range refURLs {
			refs[i] = map[string]any{"url": u}
		}
		payload["reference_images"] = refs
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errResult(err.Error())
	}
	startURL := strings.TrimRight(base, "/") + "/videos/generations"
	startCtx, cancel := context.WithTimeout(ctx, videoStartTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(startCtx, http.MethodPost, startURL, bytes.NewReader(body))
	if err != nil {
		return errResult(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return errResult("Video generation API request failed: " + err.Error())
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt := strings.ReplaceAll(string(raw), "\n", " ")
		if len(excerpt) > 200 {
			excerpt = excerpt[:200]
		}
		return errResult(fmt.Sprintf("Video generation failed with HTTP %d: %s", resp.StatusCode, excerpt))
	}
	var start struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &start); err != nil {
		preview := string(raw)
		if len(preview) > 500 {
			preview = preview[:500]
		}
		return errResult(fmt.Sprintf("Failed to parse video generation start response: %v — body preview: %s", err, preview))
	}
	if strings.TrimSpace(start.RequestID) == "" {
		return errResult("No request_id received from the video generation API.")
	}
	return c.pollAndSave(ctx, tc, base, bearer, start.RequestID)
}

func (c videoClient) pollAndSave(ctx context.Context, tc Context, base, bearer, requestID string) Result {
	pollURL := strings.TrimRight(base, "/") + "/videos/" + requestID
	deadline := time.Now().Add(videoGenTimeout)
	client := c.httpClient()
	for {
		if wait := c.PollInterval; wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return errResult(ctx.Err().Error())
			case <-timer.C:
			}
		} else {
			select {
			case <-ctx.Done():
				return errResult(ctx.Err().Error())
			default:
			}
		}
		if time.Now().After(deadline) {
			return errResult(fmt.Sprintf("Video generation did not complete within %ds (request_id=%s)", int(videoGenTimeout.Seconds()), requestID))
		}
		pollCtx, cancel := context.WithTimeout(ctx, videoPollReqTimeout)
		req, err := http.NewRequestWithContext(pollCtx, http.MethodGet, pollURL, nil)
		if err != nil {
			cancel()
			return errResult(err.Error())
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			return errResult("Video poll request failed: " + err.Error())
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			excerpt := strings.ReplaceAll(string(raw), "\n", " ")
			if len(excerpt) > 200 {
				excerpt = excerpt[:200]
			}
			return errResult(fmt.Sprintf("Video poll failed with HTTP %d: %s", resp.StatusCode, excerpt))
		}
		if resp.StatusCode == http.StatusAccepted && len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var poll struct {
			Status string `json:"status"`
			Video  *struct {
				URL     string `json:"url"`
				B64JSON string `json:"b64_json"`
				Data    string `json:"data"`
			} `json:"video"`
		}
		if err := json.Unmarshal(raw, &poll); err != nil {
			if resp.StatusCode == http.StatusAccepted {
				continue
			}
			preview := string(raw)
			if len(preview) > 500 {
				preview = preview[:500]
			}
			return errResult(fmt.Sprintf("Failed to parse video poll response: %v — body preview: %s", err, preview))
		}
		switch poll.Status {
		case "done":
			var videoURL, inline string
			if poll.Video != nil {
				videoURL = strings.TrimSpace(poll.Video.URL)
				inline = firstNonEmpty(poll.Video.B64JSON, poll.Video.Data)
			}
			var data []byte
			if inline != "" && videoURL == "" {
				decoded, err := base64.StdEncoding.DecodeString(inline)
				if err != nil {
					return errResult("Failed to decode inline video data: " + err.Error())
				}
				data = decoded
			} else {
				if videoURL == "" {
					return errResult("Video generation completed but no download URL was returned.")
				}
				bytesOut, err := c.downloadVideo(ctx, videoURL)
				if err != nil {
					return errResult(err.Error())
				}
				data = bytesOut
			}
			path, err := saveSessionVideo(tc, data)
			if err != nil {
				return errResult(err.Error())
			}
			recordAgentWrite(tc, path)
			return okResult(fmt.Sprintf(videoResultTmpl, path))
		case "failed":
			return errResult(fmt.Sprintf("Video generation failed on the server (request_id=%s): %s", requestID, truncateRunes(string(raw), 300)))
		case "expired":
			return errResult(fmt.Sprintf("Video generation request expired (request_id=%s).", requestID))
		default:
			// pending / processing / HTTP 202
		}
	}
}

func (c videoClient) downloadVideo(ctx context.Context, url string) ([]byte, error) {
	dlCtx, cancel := context.WithTimeout(ctx, videoDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("Failed to download video: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Video download failed (HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("Failed to read video bytes: %w", err)
	}
	return data, nil
}

func saveSessionVideo(tc Context, data []byte) (string, error) {
	root := tc.SessionDir
	if root == "" {
		root = tc.CWD
	}
	if root == "" {
		return "", fmt.Errorf("no session folder to save the video")
	}
	dir := filepath.Join(root, "videos")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	n := nextMediaIndex(dir)
	path := filepath.Join(dir, strconv.Itoa(n)+".mp4")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func nextMediaIndex(dir string) int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 1
	}
	maxN := 0
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".tmp") {
			continue
		}
		stem, _, ok := strings.Cut(name, ".")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(stem)
		if err == nil && n > maxN {
			maxN = n
		}
	}
	return maxN + 1
}

func resolveImageRef(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("image reference must not be empty")
	}
	if strings.HasPrefix(value, "data:image/") {
		comma := strings.IndexByte(value, ',')
		if comma < 0 {
			return "", fmt.Errorf("malformed data URL in image reference")
		}
		if !strings.Contains(value[:comma], ";base64") {
			return "", fmt.Errorf("image references only support base64 data URLs")
		}
		return value, nil
	}
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		return value, nil
	}
	raw, err := os.ReadFile(value)
	if err != nil {
		return "", fmt.Errorf("image reference not readable: %s (%v)", value, err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("image reference contained no data")
	}
	mime := sniffImageMIME(raw)
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func sniffImageMIME(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte{0x89, 'P', 'N', 'G'}):
		return "image/png"
	case bytes.HasPrefix(raw, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case bytes.HasPrefix(raw, []byte("GIF8")):
		return "image/gif"
	case len(raw) >= 12 && bytes.HasPrefix(raw, []byte("RIFF")) && bytes.Equal(raw[8:12], []byte("WEBP")):
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

func parseVideoDuration(v any) (int, error) {
	if v == nil {
		return defaultVideoDuration, nil
	}
	n, ok := coerceInt(v)
	if !ok {
		return 0, fmt.Errorf("`duration` must be either 6 or 10 seconds. Got %v.", v)
	}
	for _, d := range validVideoDurations {
		if n == d {
			return n, nil
		}
	}
	return 0, fmt.Errorf("`duration` must be either 6 or 10 seconds. Got %d.", n)
}

func parseVideoResolution(v any) (string, error) {
	s := strings.TrimSpace(str(v))
	if s == "" {
		return defaultVideoRes, nil
	}
	for _, a := range validVideoRes {
		if s == a {
			return s, nil
		}
	}
	return "", fmt.Errorf("`resolution_name` must be one of: %s. Got %s.", strings.Join(validVideoRes, ", "), s)
}

func parseVideoAspect(v any) (string, error) {
	s := strings.TrimSpace(str(v))
	if s == "" {
		return "", fmt.Errorf("`aspect_ratio` must be one of: %s. Got %s.", strings.Join(validVideoAspects, ", "), s)
	}
	for _, a := range validVideoAspects {
		if s == a {
			return s, nil
		}
	}
	return "", fmt.Errorf("`aspect_ratio` must be one of: %s. Got %s.", strings.Join(validVideoAspects, ", "), s)
}

func coerceInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		if x != float64(int(x)) {
			return 0, false
		}
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func stringList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			out = append(out, strings.TrimSpace(str(item)))
		}
		return out
	default:
		return nil
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
