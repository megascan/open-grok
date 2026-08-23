package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/tools"
)

func TestReferenceToVideoSavesMP4AndRecordsWrite(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	session := t.TempDir()
	mp4 := []byte("ref-mp4")
	srv, probe := startVideoAPI(t, mp4)
	args, _ := json.Marshal(map[string]any{
		"prompt":          "blend these into a cinematic shot",
		"images":          []string{pngDataURL, pngDataURL},
		"aspect_ratio":    "16:9",
		"duration":        6,
		"resolution_name": "480p",
	})
	res := tools.ReferenceToVideo{
		HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "xai-key", PollInterval: 0,
	}.Execute(context.Background(), tools.Context{SessionDir: session}, args)
	if res.IsError {
		t.Fatal(res.Content)
	}
	want := filepath.Join(session, "videos", "1.mp4")
	if !strings.Contains(res.Content, want) {
		t.Fatalf("result %s", res.Content)
	}
	if !strings.Contains(res.Content, "Do not read or re-display it") {
		t.Fatalf("rust result prose missing: %s", res.Content)
	}
	got, err := os.ReadFile(want)
	if err != nil || string(got) != string(mp4) {
		t.Fatalf("saved %q err=%v", got, err)
	}
	_, _, _, auth, body := probe.snapshot()
	if auth != "Bearer xai-key" {
		t.Fatalf("auth %q", auth)
	}
	if body["model"] != "grok-imagine-video" || body["prompt"] != "blend these into a cinematic shot" {
		t.Fatalf("body %+v", body)
	}
	if body["aspect_ratio"] != "16:9" || body["duration"] != float64(6) || body["resolution"] != "480p" {
		t.Fatalf("fields %+v", body)
	}
	if _, ok := body["image"]; ok {
		t.Fatalf("reference_to_video must omit image: %+v", body)
	}
	refs, _ := body["reference_images"].([]any)
	if len(refs) != 2 {
		t.Fatalf("reference_images %+v", body["reference_images"])
	}
	if !tools.AgentWrote(want) {
		t.Fatal("video write must be attributed to the agent")
	}
}

func TestReferenceToVideoRejectsTooFewImages(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	args, _ := json.Marshal(map[string]any{
		"prompt":       "blend",
		"images":       []string{pngDataURL},
		"aspect_ratio": "16:9",
	})
	res := tools.ReferenceToVideo{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, args)
	if !res.IsError || !strings.Contains(res.Content, "at least two") {
		t.Fatalf("%s", res.Content)
	}
}

func TestReferenceToVideoRejectsTooManyImages(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	imgs := make([]string, 8)
	for i := range imgs {
		imgs[i] = pngDataURL
	}
	args, _ := json.Marshal(map[string]any{
		"prompt":       "blend",
		"images":       imgs,
		"aspect_ratio": "16:9",
	})
	res := tools.ReferenceToVideo{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, args)
	if !res.IsError || !strings.Contains(res.Content, "at most") {
		t.Fatalf("%s", res.Content)
	}
}

func TestReferenceToVideoRejectsBadAspect(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	args, _ := json.Marshal(map[string]any{
		"prompt":       "blend",
		"images":       []string{pngDataURL, pngDataURL},
		"aspect_ratio": "4:3",
	})
	res := tools.ReferenceToVideo{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, args)
	if !res.IsError || !strings.Contains(res.Content, "aspect_ratio") {
		t.Fatalf("%s", res.Content)
	}
}

func TestReferenceToVideoRequiresPrompt(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	args, _ := json.Marshal(map[string]any{
		"images":       []string{pngDataURL, pngDataURL},
		"aspect_ratio": "16:9",
	})
	res := tools.ReferenceToVideo{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, args)
	if !res.IsError || !strings.Contains(res.Content, "prompt") {
		t.Fatalf("%s", res.Content)
	}
}

func TestVideoGenDownloadsInlineBytes(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	session := t.TempDir()
	srv, probe := startVideoAPI(t, nil)
	probe.mu.Lock()
	probe.inlineB64 = "aW5saW5lLW1wNA==" // inline-mp4
	probe.mu.Unlock()
	res := tools.ImageToVideo{
		HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "k", PollInterval: 0,
	}.Execute(context.Background(), tools.Context{SessionDir: session}, []byte(`{"image":"`+pngDataURL+`"}`))
	if res.IsError {
		t.Fatal(res.Content)
	}
	want := filepath.Join(session, "videos", "1.mp4")
	got, err := os.ReadFile(want)
	if err != nil || string(got) != "inline-mp4" {
		t.Fatalf("saved %q err=%v", got, err)
	}
	_, _, downloads, _, _ := probe.snapshot()
	if downloads != 0 {
		t.Fatalf("inline path must not GET the clip URL, downloads=%d", downloads)
	}
	if !tools.AgentWrote(want) {
		t.Fatal("inline video write must be attributed to the agent")
	}
}
