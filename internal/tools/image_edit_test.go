package tools_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/tools"
)

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeJPEG(t *testing.T, dir, name string) (string, []byte) {
	t.Helper()
	jpegBytes := tinyJPEG(t)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, jpegBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, jpegBytes
}

func TestImageEditSavesGrokImagineJPEG(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	session := t.TempDir()
	refDir := t.TempDir()
	refPath, jpegBytes := writeJPEG(t, refDir, "ref.jpg")
	var gotBody map[string]any
	var gotAuth string
	var gotRaw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Errorf("path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotRaw, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(gotRaw, &gotBody)
		b64 := base64.StdEncoding.EncodeToString([]byte("edited-jpeg"))
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64 + `"}]}`))
	}))
	t.Cleanup(srv.Close)

	args, _ := json.Marshal(map[string]any{
		"prompt": "anime style",
		"image":  []string{refPath},
	})
	res := tools.ImageEdit{
		HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "xai-key", Provider: "grok",
	}.Execute(context.Background(), tools.Context{SessionDir: session}, args)
	if res.IsError {
		t.Fatal(res.Content)
	}
	want := filepath.Join(session, "images", "1.jpg")
	if !strings.Contains(res.Content, want) {
		t.Fatalf("result %s", res.Content)
	}
	if !strings.Contains(res.Content, "Do not read or re-display it") {
		t.Fatalf("rust result prose missing: %s", res.Content)
	}
	got, err := os.ReadFile(want)
	if err != nil || string(got) != "edited-jpeg" {
		t.Fatalf("saved %q err=%v", got, err)
	}
	if gotAuth != "Bearer xai-key" {
		t.Fatalf("auth %q", gotAuth)
	}
	if gotBody["model"] != "grok-imagine-image-quality" || gotBody["prompt"] != "anime style" {
		t.Fatalf("body %+v", gotBody)
	}
	if gotBody["resolution"] != "1k" || gotBody["response_format"] != "b64_json" {
		t.Fatalf("grok fields %+v", gotBody)
	}
	if _, ok := gotBody["aspect_ratio"]; ok {
		t.Fatalf("single-ref grok payload must omit aspect_ratio: %+v", gotBody)
	}
	img, _ := gotBody["image"].(map[string]any)
	url, _ := img["url"].(string)
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("image url %q", url)
	}
	gotJPEG, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/jpeg;base64,"))
	if err != nil || !bytes.Equal(gotJPEG, jpegBytes) {
		t.Fatal("small jpeg must pass through uncompressed in the request body")
	}
	if !bytes.Contains(gotRaw, []byte(base64.StdEncoding.EncodeToString(jpegBytes))) {
		t.Fatal("request body missing original jpeg bytes")
	}
	if !tools.AgentWrote(want) {
		t.Fatal("image write must be attributed to the agent")
	}
}

func TestImageEditNumbersSequentialFiles(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	session := t.TempDir()
	if err := os.MkdirAll(filepath.Join(session, "images"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session, "images", "1.jpg"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	refPath, _ := writeJPEG(t, t.TempDir(), "ref.jpg")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b64 := base64.StdEncoding.EncodeToString([]byte("two"))
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64 + `"}]}`))
	}))
	t.Cleanup(srv.Close)
	args, _ := json.Marshal(map[string]any{"prompt": "next", "image": []string{refPath}})
	res := tools.ImageEdit{HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "k", Provider: "grok"}.
		Execute(context.Background(), tools.Context{SessionDir: session}, args)
	if res.IsError || !strings.Contains(res.Content, filepath.Join(session, "images", "2.jpg")) {
		t.Fatalf("%s", res.Content)
	}
}

func TestImageEditOpenAIContract(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	session := t.TempDir()
	jpegBytes := tinyJPEG(t)
	dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegBytes)
	var gotBody map[string]any
	var gotTurn string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Errorf("path %s", r.URL.Path)
		}
		gotTurn = r.Header.Get("x-codex-image-turn-id")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		b64 := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64 + `"}]}`))
	}))
	t.Cleanup(srv.Close)
	args, _ := json.Marshal(map[string]any{
		"prompt":       "make it blue",
		"image":        []string{dataURL},
		"aspect_ratio": "16:9",
	})
	res := tools.ImageEdit{HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "codex", Provider: "openai"}.
		Execute(context.Background(), tools.Context{SessionDir: session}, args)
	if res.IsError {
		t.Fatal(res.Content)
	}
	want := filepath.Join(session, "images", "1.png")
	if !strings.Contains(res.Content, want) {
		t.Fatalf("%s", res.Content)
	}
	if gotTurn != "image_edit" {
		t.Fatalf("turn header %q", gotTurn)
	}
	if gotBody["model"] != "gpt-image-2" || gotBody["size"] != "1536x1024" {
		t.Fatalf("openai body %+v", gotBody)
	}
	if gotBody["background"] != "auto" || gotBody["quality"] != "auto" {
		t.Fatalf("openai fields %+v", gotBody)
	}
	images, _ := gotBody["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("images %+v", gotBody["images"])
	}
	first, _ := images[0].(map[string]any)
	if first["image_url"] != dataURL {
		t.Fatalf("image_url %+v", first)
	}
}

func TestImageEditRequiresImagesAndPrompt(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	res := tools.ImageEdit{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, []byte(`{"prompt":"x","image":[]}`))
	want := "image_edit requires at least one reference image. Use image_gen for text-only generation."
	if !res.IsError || res.Content != want {
		t.Fatalf("empty image: %q", res.Content)
	}
	res = tools.ImageEdit{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, []byte(`{"image":["/tmp/x.jpg"]}`))
	if !res.IsError || !strings.Contains(res.Content, "prompt is required") {
		t.Fatalf("empty prompt: %s", res.Content)
	}
}

func TestImageEditMissingFile(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	missing := filepath.Join(t.TempDir(), "no-such.jpg")
	args, _ := json.Marshal(map[string]any{"prompt": "edit", "image": []string{missing}})
	res := tools.ImageEdit{Provider: "grok", Bearer: "k", BaseURL: "http://127.0.0.1:1"}.
		Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, args)
	if !res.IsError || !strings.Contains(res.Content, "not readable") {
		t.Fatalf("%s", res.Content)
	}
}

func TestImageEditResolvesAttachmentAndFileURI(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	session := t.TempDir()
	refPath, jpegBytes := writeJPEG(t, t.TempDir(), "att.jpg")
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		img, _ := body["image"].(map[string]any)
		gotURL, _ = img["url"].(string)
		b64 := base64.StdEncoding.EncodeToString([]byte("ok"))
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64 + `"}]}`))
	}))
	t.Cleanup(srv.Close)

	args, _ := json.Marshal(map[string]any{"prompt": "remix", "image": []string{"[Image #1]"}})
	res := tools.ImageEdit{
		HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "k", Provider: "grok",
		Attachments: map[int]string{1: "file://" + refPath},
	}.Execute(context.Background(), tools.Context{SessionDir: session}, args)
	if res.IsError {
		t.Fatal(res.Content)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(gotURL, "data:image/jpeg;base64,"))
	if err != nil || !bytes.Equal(decoded, jpegBytes) {
		t.Fatalf("attachment/file URI passthrough failed url=%s", gotURL)
	}
}
