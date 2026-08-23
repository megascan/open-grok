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
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
)

const (
	xaiImagineModel    = "grok-imagine-image-quality"
	openaiImageModel   = "gpt-image-2"
	imageGenTimeout    = 300 * time.Second
	codexImageTurnHdr  = "x-codex-image-turn-id"
	imageGenResultTmpl = "Image generated and saved to %s. Do not read or re-display it, and do not describe how it appears to the user."
)

// ImageGen is the rust image_gen tool: POST /images/generations and save
// session/images/<n>.jpg (Grok Imagine) or .png (OpenAI Images).
type ImageGen struct {
	HTTP     *http.Client
	BaseURL  string
	Bearer   string
	Provider string
	Model    string
}

func (ImageGen) Name() string { return "image_gen" }

func (ImageGen) Description() string {
	return "Generate a new image from a text description using the configured image provider; returns the saved image's absolute path. When telling the user where it was saved, refer to it by its short session-relative path (for example `images/1.png`) rather than the absolute path, so it renders as a clickable link that opens the image. To produce multiple images, emit multiple tool calls with distinct prompts."
}

func (ImageGen) Access() types.AccessKind { return types.AccessEdit }

func (ImageGen) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"prompt": map[string]any{
			"type":        "string",
			"description": "Text description of the image to generate.",
		},
		"aspect_ratio": map[string]any{
			"type":        "string",
			"description": "Aspect ratio of the generated image, decide it based on the user's request. Defaults to 'auto'. 1:1 for square (icons, profiles), 16:9 for wide (landscapes, cinematic), 9:16 for tall (phone wallpapers, stories), 3:2 for horizontal photos, 2:3 for vertical (portraits, posters).",
		},
	}, []string{"prompt"})
}

func (g ImageGen) Execute(ctx context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	prompt := strings.TrimSpace(str(m["prompt"]))
	if prompt == "" {
		return errResult("prompt is required")
	}
	aspect := strings.TrimSpace(str(m["aspect_ratio"]))
	if aspect == "" {
		aspect = "auto"
	}
	provider := g.resolvedProvider()
	base, bearer, model, ext, err := g.resolveRoute(provider)
	if err != nil {
		return errResult(err.Error())
	}
	payload := imageGenPayload(provider, model, prompt, aspect)
	body, err := json.Marshal(payload)
	if err != nil {
		return errResult(err.Error())
	}
	url := strings.TrimRight(base, "/") + "/images/generations"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return errResult(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	if provider == "openai" {
		req.Header.Set(codexImageTurnHdr, "image_gen")
	}
	client := g.HTTP
	if client == nil {
		client = &http.Client{Timeout: imageGenTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return errResult("Image generation API request failed: " + err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt := strings.ReplaceAll(string(raw), "\n", " ")
		if len(excerpt) > 200 {
			excerpt = excerpt[:200]
		}
		return errResult(fmt.Sprintf("Image generation failed with HTTP %d: %s", resp.StatusCode, excerpt))
	}
	bytesOut, err := decodeImageB64(raw)
	if err != nil {
		return errResult(err.Error())
	}
	path, err := saveSessionImage(tc, bytesOut, ext)
	if err != nil {
		return errResult(err.Error())
	}
	recordAgentWrite(tc, path)
	return okResult(fmt.Sprintf(imageGenResultTmpl, path))
}

func (g ImageGen) resolvedProvider() string {
	p := strings.ToLower(strings.TrimSpace(g.Provider))
	if p == "openai" || p == "grok" {
		return p
	}
	cfg, _ := config.Load()
	p = strings.ToLower(strings.TrimSpace(cfg.UI.ImageGen))
	if p == "openai" {
		return "openai"
	}
	return "grok"
}

func (g ImageGen) resolveRoute(provider string) (base, bearer, model, ext string, err error) {
	ext = "jpg"
	model = xaiImagineModel
	prov := types.ProviderXAI
	if provider == "openai" {
		ext = "png"
		model = openaiImageModel
		prov = types.ProviderCodex
	}
	if g.Model != "" {
		model = g.Model
	}
	base = strings.TrimSpace(g.BaseURL)
	if base == "" {
		base = strings.TrimSpace(os.Getenv("ENTROPY_IMAGE_GEN_BASE_URL"))
	}
	if base == "" {
		base = catalog.DefaultBaseURL(prov)
	}
	bearer = strings.TrimSpace(g.Bearer)
	if bearer == "" {
		token, _, e := auth.ResolveBearer(prov, auth.EnvKeys(prov), false)
		if e != nil || token == "" {
			if provider == "openai" {
				return "", "", "", "", fmt.Errorf("OpenAI image authentication is unavailable; sign in again with `open-grok login --codex`")
			}
			return "", "", "", "", fmt.Errorf("not signed in to xAI (try `entropy login xai`)")
		}
		bearer = token
	}
	return base, bearer, model, ext, nil
}

func imageGenPayload(provider, model, prompt, aspect string) map[string]any {
	if provider == "openai" {
		return map[string]any{
			"model":      model,
			"prompt":     prompt,
			"background": "auto",
			"quality":    "auto",
			"size":       openaiImageSize(aspect),
		}
	}
	return map[string]any{
		"model":           model,
		"prompt":          prompt,
		"n":               1,
		"aspect_ratio":    aspect,
		"resolution":      "1k",
		"response_format": "b64_json",
	}
}

func openaiImageSize(aspect string) string {
	switch strings.TrimSpace(aspect) {
	case "16:9", "3:2", "4:3", "2:1", "19.5:9", "20:9":
		return "1536x1024"
	case "9:16", "2:3", "3:4", "1:2", "9:19.5", "9:20":
		return "1024x1536"
	case "1:1":
		return "1024x1024"
	default:
		return "auto"
	}
}

func decodeImageB64(raw []byte) ([]byte, error) {
	var parsed struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		preview := string(raw)
		if len(preview) > 500 {
			preview = preview[:500]
		}
		return nil, fmt.Errorf("Failed to parse image generation response: %v — body preview: %s", err, preview)
	}
	if len(parsed.Data) == 0 || parsed.Data[0].B64JSON == "" {
		return nil, fmt.Errorf("Image generation returned no image data.")
	}
	out, err := base64.StdEncoding.DecodeString(parsed.Data[0].B64JSON)
	if err != nil {
		return nil, fmt.Errorf("Failed to decode base64 image data: %v", err)
	}
	return out, nil
}

func saveSessionImage(tc Context, data []byte, ext string) (string, error) {
	root := tc.SessionDir
	if root == "" {
		root = tc.CWD
	}
	if root == "" {
		return "", fmt.Errorf("no session folder to save the image")
	}
	dir := filepath.Join(root, "images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	n := nextImageIndex(dir)
	path := filepath.Join(dir, strconv.Itoa(n)+"."+ext)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func nextImageIndex(dir string) int {
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
