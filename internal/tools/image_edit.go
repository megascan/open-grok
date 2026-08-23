package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/megascan/open-grok/internal/types"
)

const (
	imageEditResultTmpl = "Image edited and saved to %s. Do not read or re-display it, and do not describe how it appears to the user."
	maxRefRawBytes      = 400 * 1024
	maxRefSidePx        = 768
	minRefSidePx        = 256
	maxRefDecodePixels  = 12_000_000
	emptyImageEditErr   = "image_edit requires at least one reference image. Use image_gen for text-only generation."
	openaiEditMaxRefs   = 5
)

var refJPEGQualities = []int{80, 65, 50, 35}

// ImageEdit is the rust image_edit tool: POST /images/edits with one or more
// reference images and save the result under session/images/.
type ImageEdit struct {
	HTTP        *http.Client
	BaseURL     string
	Bearer      string
	Provider    string
	Model       string
	Attachments map[int]string // optional: [Image #N] → path or data URL
}

func (e ImageEdit) asGen() ImageGen {
	return ImageGen{
		HTTP:     e.HTTP,
		BaseURL:  e.BaseURL,
		Bearer:   e.Bearer,
		Provider: e.Provider,
		Model:    e.Model,
	}
}

func (ImageEdit) Name() string { return "image_edit" }

func (ImageEdit) Description() string {
	return "Edit or transform existing image(s) via the configured image provider; use instead of image_gen for image-to-image work (preserve likeness, transfer style, remix). Returns the saved image's absolute path. When telling the user where it was saved, refer to it by its short session-relative path (for example `images/1.png`) rather than the absolute path, so it renders as a clickable link that opens the image. Each required `image` is one reference — a user-attachment token (for example \"[Image #1]\"), an absolute filesystem path, or a `data:image/...;base64,...` URL (see the `image` parameter for the resolution order and details)."
}

func (ImageEdit) Access() types.AccessKind { return types.AccessEdit }

func (ImageEdit) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"prompt": map[string]any{
			"type":        "string",
			"description": "A text description of the desired edit or transformation. Describe what the output image should look like, referencing the input image(s).",
		},
		"image": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Reference image(s) to condition the edit on. Each is one reference, in priority order: (1) a user attachment — its placeholder token, e.g. \"[Image #1]\" (attachments have no path you can see, so never invent one); (2) an absolute filesystem path the user gave you; (3) a `data:image/...;base64,...` URL.",
		},
		"aspect_ratio": map[string]any{
			"type":        "string",
			"description": "The aspect ratio of the output image. For single-image edits this is ignored — the output matches the input image's aspect ratio. For multi-image edits, defaults to 'auto'. Supported values: 1:1, 16:9, 9:16, 4:3, 3:4, 3:2, 2:3, 2:1, 1:2, 19.5:9, 9:19.5, 20:9, 9:20, auto.",
		},
	}, []string{"prompt", "image"})
}

func (e ImageEdit) Execute(ctx context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	refs := imageRefList(m["image"])
	if len(refs) == 0 {
		return errResult(emptyImageEditErr)
	}
	prompt := strings.TrimSpace(str(m["prompt"]))
	if prompt == "" {
		return errResult("prompt is required")
	}
	aspect := strings.TrimSpace(str(m["aspect_ratio"]))
	if aspect == "" {
		aspect = "auto"
	}
	provider := e.asGen().resolvedProvider()
	if provider == "openai" && len(refs) > openaiEditMaxRefs {
		return errResult("OpenAI image editing accepts at most 5 reference images.")
	}
	dataURLs := make([]string, 0, len(refs))
	for _, ref := range refs {
		resolved, err := resolveAttachmentReference(ref, e.Attachments)
		if err != nil {
			return errResult(err.Error())
		}
		url, err := resolveToDataURL(resolved)
		if err != nil {
			return errResult(err.Error())
		}
		dataURLs = append(dataURLs, url)
	}
	base, bearer, model, ext, err := e.asGen().resolveRoute(provider)
	if err != nil {
		return errResult(err.Error())
	}
	payload := imageEditPayload(provider, model, prompt, aspect, dataURLs)
	body, err := json.Marshal(payload)
	if err != nil {
		return errResult(err.Error())
	}
	url := strings.TrimRight(base, "/") + "/images/edits"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return errResult(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	if provider == "openai" {
		req.Header.Set(codexImageTurnHdr, "image_edit")
	}
	client := e.HTTP
	if client == nil {
		client = &http.Client{Timeout: imageGenTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return errResult("Image edit API request failed: " + err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt := strings.ReplaceAll(string(raw), "\n", " ")
		if len(excerpt) > 200 {
			excerpt = excerpt[:200]
		}
		return errResult(fmt.Sprintf("Image edit failed with HTTP %d: %s", resp.StatusCode, excerpt))
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
	return okResult(fmt.Sprintf(imageEditResultTmpl, path))
}

func imageEditPayload(provider, model, prompt, aspect string, dataURLs []string) map[string]any {
	if provider == "openai" {
		images := make([]any, 0, len(dataURLs))
		for _, u := range dataURLs {
			images = append(images, map[string]any{"image_url": u})
		}
		return map[string]any{
			"model":      model,
			"prompt":     prompt,
			"background": "auto",
			"quality":    "auto",
			"size":       openaiImageSize(aspect),
			"images":     images,
		}
	}
	payload := map[string]any{
		"model":           model,
		"prompt":          prompt,
		"n":               1,
		"resolution":      "1k",
		"response_format": "b64_json",
	}
	refs := make([]any, 0, len(dataURLs))
	for _, u := range dataURLs {
		refs = append(refs, map[string]any{"url": u})
	}
	if len(refs) == 1 {
		payload["image"] = refs[0]
	} else {
		payload["images"] = refs
		payload["aspect_ratio"] = aspect
	}
	return payload
}

func imageRefList(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			out = append(out, str(item))
		}
		return out
	case []string:
		return append([]string(nil), x...)
	default:
		return nil
	}
}

func parseAttachmentToken(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	inner := trimmed
	if strings.HasPrefix(inner, "[") && strings.HasSuffix(inner, "]") && len(inner) >= 2 {
		inner = strings.TrimSpace(inner[1 : len(inner)-1])
	}
	rest := inner
	if len(inner) >= 5 && strings.EqualFold(inner[:5], "image") {
		rest = strings.TrimSpace(inner[5:])
	}
	if !strings.HasPrefix(rest, "#") {
		return 0, false
	}
	digits := strings.TrimSpace(rest[1:])
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

func resolveAttachmentReference(reference string, attached map[int]string) (string, error) {
	n, ok := parseAttachmentToken(reference)
	if !ok {
		return reference, nil
	}
	if len(attached) == 0 {
		return "", fmt.Errorf("image reference %q matches no image attached to this message. If it was attached earlier in the conversation, ask the user to re-attach it here; otherwise pass an absolute filesystem path or a data: URL.", reference)
	}
	path, ok := attached[n]
	if !ok {
		keys := make([]int, 0, len(attached))
		for k := range attached {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		avail := make([]string, 0, len(keys))
		for _, k := range keys {
			avail = append(avail, fmt.Sprintf("[Image #%d]", k))
		}
		return "", fmt.Errorf("image reference %q does not match any attached image. Available: %s.", reference, strings.Join(avail, ", "))
	}
	return path, nil
}

func resolveToDataURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "file://")
	var raw []byte
	var err error
	if strings.HasPrefix(value, "data:image/") {
		comma := strings.IndexByte(value, ',')
		if comma < 0 {
			return "", fmt.Errorf("malformed data URL in image reference")
		}
		if !strings.Contains(value[:comma], ";base64") {
			return "", fmt.Errorf("image references only support base64 data URLs")
		}
		raw, err = base64.StdEncoding.DecodeString(value[comma+1:])
		if err != nil {
			return "", fmt.Errorf("invalid base64 in image reference: %v", err)
		}
	} else {
		raw, err = os.ReadFile(value)
		if err != nil {
			return "", fmt.Errorf("image reference not readable: %s (%v)", value, err)
		}
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("image reference contained no data")
	}
	compressed, mime, err := compressReference(raw)
	if err != nil {
		return "", err
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(compressed), nil
}

func sniffJPEG(b []byte) bool {
	return len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff
}

func sniffPNG(b []byte) bool {
	return len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
}

func compressReference(raw []byte) ([]byte, string, error) {
	if len(raw) <= maxRefRawBytes {
		if sniffJPEG(raw) {
			return raw, "image/jpeg", nil
		}
		if sniffPNG(raw) {
			return raw, "image/png", nil
		}
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("could not detect image format for reference")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxRefDecodePixels {
		return nil, "", fmt.Errorf("image reference is too large to process (%d×%d pixels)", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode image reference")
	}
	out, mime, err := reEncodeUnderLimit(img)
	if err != nil {
		return nil, "", fmt.Errorf("could not compress image reference small enough for Imagine API: %v", err)
	}
	return out, mime, nil
}

func reEncodeUnderLimit(src image.Image) ([]byte, string, error) {
	rgba := toRGBA(src)
	ow, oh := rgba.Bounds().Dx(), rgba.Bounds().Dy()
	origMax := ow
	if oh > origMax {
		origMax = oh
	}
	maxSide := maxRefSidePx
	if origMax < maxSide {
		maxSide = origMax
	}
	var lastSide int
	for {
		scaled := resizeMaxSide(rgba, maxSide)
		w, h := scaled.Bounds().Dx(), scaled.Bounds().Dy()
		lastSide = w
		if h > lastSide {
			lastSide = h
		}
		var pngBuf, jpegBuf []byte
		if b, err := encodePNG(scaled); err == nil && len(b) <= maxRefRawBytes {
			pngBuf = b
		}
		for _, q := range refJPEGQualities {
			b, err := encodeJPEG(scaled, q)
			if err == nil && len(b) <= maxRefRawBytes {
				jpegBuf = b
				break
			}
		}
		switch {
		case pngBuf != nil && jpegBuf != nil:
			if len(pngBuf) <= len(jpegBuf) {
				return pngBuf, "image/png", nil
			}
			return jpegBuf, "image/jpeg", nil
		case pngBuf != nil:
			return pngBuf, "image/png", nil
		case jpegBuf != nil:
			return jpegBuf, "image/jpeg", nil
		}
		if maxSide <= minRefSidePx {
			return nil, "", fmt.Errorf("re-encode could not fit under %d bytes after PNG+JPEG attempts (last side %dpx)", maxRefRawBytes, lastSide)
		}
		maxSide = maxSide * 3 / 4
	}
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok && r.Bounds().Min == image.Pt(0, 0) {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func resizeMaxSide(src *image.RGBA, maxSide int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if w <= 0 || h <= 0 || maxSide < 1 {
		return src
	}
	origMax := w
	if h > origMax {
		origMax = h
	}
	if origMax <= maxSide {
		return src
	}
	nw := int(math.Round(float64(w) * float64(maxSide) / float64(origMax)))
	nh := int(math.Round(float64(h) * float64(maxSide) / float64(origMax)))
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := (float64(y)+0.5)*float64(h)/float64(nh) - 0.5
		for x := 0; x < nw; x++ {
			sx := (float64(x)+0.5)*float64(w)/float64(nw) - 0.5
			dst.SetRGBA(x, y, sampleBilinear(src, sx, sy, w, h))
		}
	}
	return dst
}

func sampleBilinear(src *image.RGBA, x, y float64, w, h int) color.RGBA {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	maxX := float64(w - 1)
	maxY := float64(h - 1)
	if x > maxX {
		x = maxX
	}
	if y > maxY {
		y = maxY
	}
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	x1 := x0 + 1
	y1 := y0 + 1
	if x1 >= w {
		x1 = w - 1
	}
	if y1 >= h {
		y1 = h - 1
	}
	fx := x - float64(x0)
	fy := y - float64(y0)
	c00 := src.RGBAAt(x0, y0)
	c10 := src.RGBAAt(x1, y0)
	c01 := src.RGBAAt(x0, y1)
	c11 := src.RGBAAt(x1, y1)
	mix8 := func(a, b uint8, t float64) float64 {
		return float64(a)*(1-t) + float64(b)*t
	}
	mixf := func(a, b, t float64) float64 {
		return a*(1-t) + b*t
	}
	r := mixf(mix8(c00.R, c10.R, fx), mix8(c01.R, c11.R, fx), fy)
	g := mixf(mix8(c00.G, c10.G, fx), mix8(c01.G, c11.G, fx), fy)
	b := mixf(mix8(c00.B, c10.B, fx), mix8(c01.B, c11.B, fx), fy)
	a := mixf(mix8(c00.A, c10.A, fx), mix8(c01.A, c11.A, fx), fy)
	return color.RGBA{R: uint8(r + 0.5), G: uint8(g + 0.5), B: uint8(b + 0.5), A: uint8(a + 0.5)}
}
