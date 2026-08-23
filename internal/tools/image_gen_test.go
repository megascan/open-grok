package tools_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

func TestImageGenSavesGrokImagineJPEG(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	session := t.TempDir()
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		b64 := base64.StdEncoding.EncodeToString([]byte("jpeg-bytes"))
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64 + `"}]}`))
	}))
	t.Cleanup(srv.Close)

	res := tools.ImageGen{
		HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "xai-key", Provider: "grok",
	}.Execute(context.Background(), tools.Context{SessionDir: session}, []byte(`{"prompt":"a red fox","aspect_ratio":"16:9"}`))
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
	if err != nil || string(got) != "jpeg-bytes" {
		t.Fatalf("saved %q err=%v", got, err)
	}
	if gotAuth != "Bearer xai-key" {
		t.Fatalf("auth %q", gotAuth)
	}
	if gotBody["model"] != "grok-imagine-image-quality" || gotBody["prompt"] != "a red fox" {
		t.Fatalf("body %+v", gotBody)
	}
	if gotBody["aspect_ratio"] != "16:9" || gotBody["resolution"] != "1k" || gotBody["response_format"] != "b64_json" {
		t.Fatalf("grok fields %+v", gotBody)
	}
	if !tools.AgentWrote(want) {
		t.Fatal("image write must be attributed to the agent")
	}
}

func TestImageGenNumbersSequentialFiles(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	session := t.TempDir()
	if err := os.MkdirAll(filepath.Join(session, "images"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session, "images", "1.jpg"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b64 := base64.StdEncoding.EncodeToString([]byte("two"))
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64 + `"}]}`))
	}))
	t.Cleanup(srv.Close)
	res := tools.ImageGen{HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "k", Provider: "grok"}.
		Execute(context.Background(), tools.Context{SessionDir: session}, []byte(`{"prompt":"next"}`))
	if res.IsError || !strings.Contains(res.Content, filepath.Join(session, "images", "2.jpg")) {
		t.Fatalf("%s", res.Content)
	}
}

func TestImageGenOpenAIContract(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	session := t.TempDir()
	var gotBody map[string]any
	var gotTurn string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTurn = r.Header.Get("x-codex-image-turn-id")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		b64 := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64 + `"}]}`))
	}))
	t.Cleanup(srv.Close)
	res := tools.ImageGen{HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "codex", Provider: "openai"}.
		Execute(context.Background(), tools.Context{SessionDir: session}, []byte(`{"prompt":"a red fox","aspect_ratio":"16:9"}`))
	if res.IsError {
		t.Fatal(res.Content)
	}
	want := filepath.Join(session, "images", "1.png")
	if !strings.Contains(res.Content, want) {
		t.Fatalf("%s", res.Content)
	}
	if gotTurn != "image_gen" {
		t.Fatalf("turn header %q", gotTurn)
	}
	if gotBody["model"] != "gpt-image-2" || gotBody["size"] != "1536x1024" {
		t.Fatalf("openai body %+v", gotBody)
	}
}

func TestImageGenRequiresPromptAndCredentials(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	_ = os.Unsetenv("XAI_API_KEY")
	res := tools.ImageGen{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, []byte(`{}`))
	if !res.IsError || !strings.Contains(res.Content, "prompt is required") {
		t.Fatalf("%s", res.Content)
	}
	res = tools.ImageGen{Provider: "grok"}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, []byte(`{"prompt":"x"}`))
	if !res.IsError || !strings.Contains(strings.ToLower(res.Content), "not signed in") {
		t.Fatalf("%s", res.Content)
	}
}

func TestDefaultPackIncludesImageGen(t *testing.T) {
	if tools.Lookup(tools.DefaultPack(), "image_gen") == nil {
		t.Fatal("image_gen must be in DefaultPack so /imagine can call it")
	}
	if tools.Lookup(tools.NestedPack(), "image_gen") == nil {
		t.Fatal("image_gen must be callable from Code Mode nested tools.*")
	}
}
