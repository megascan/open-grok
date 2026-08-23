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

const pngDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func TestImageToVideoSavesMP4AndRecordsWrite(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	session := t.TempDir()
	mp4 := []byte("fake-mp4-bytes")
	srv, probe := startVideoAPI(t, mp4)

	res := tools.ImageToVideo{
		HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "xai-key", PollInterval: 0,
	}.Execute(context.Background(), tools.Context{SessionDir: session}, []byte(`{"image":"`+pngDataURL+`","prompt":"wind in the hair"}`))
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
	posts, polls, downloads, auth, body := probe.snapshot()
	if posts != 1 || polls < 2 || downloads != 1 {
		t.Fatalf("http counts posts=%d polls=%d downloads=%d", posts, polls, downloads)
	}
	if auth != "Bearer xai-key" {
		t.Fatalf("auth %q", auth)
	}
	if body["model"] != "grok-imagine-video-1.5-preview" || body["prompt"] != "wind in the hair" {
		t.Fatalf("body %+v", body)
	}
	if body["duration"] != float64(6) || body["resolution"] != "480p" {
		t.Fatalf("defaults %+v", body)
	}
	img, _ := body["image"].(map[string]any)
	if img["url"] != pngDataURL {
		t.Fatalf("image %+v", body["image"])
	}
	if _, ok := body["aspect_ratio"]; ok {
		t.Fatalf("image_to_video must omit aspect_ratio: %+v", body)
	}
	if !tools.AgentWrote(want) {
		t.Fatal("video write must be attributed to the agent")
	}
}

func TestImageToVideoNumbersSequentialFiles(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	session := t.TempDir()
	if err := os.MkdirAll(filepath.Join(session, "videos"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session, "videos", "1.mp4"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, _ := startVideoAPI(t, []byte("two"))
	res := tools.ImageToVideo{
		HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "k", PollInterval: 0,
	}.Execute(context.Background(), tools.Context{SessionDir: session}, []byte(`{"image":"`+pngDataURL+`"}`))
	if res.IsError || !strings.Contains(res.Content, filepath.Join(session, "videos", "2.mp4")) {
		t.Fatalf("%s", res.Content)
	}
}

func TestImageToVideoRejectsBadDuration(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	res := tools.ImageToVideo{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, []byte(`{"image":"`+pngDataURL+`","duration":8}`))
	if !res.IsError || !strings.Contains(res.Content, "6 or 10") {
		t.Fatalf("%s", res.Content)
	}
}

func TestImageToVideoRejectsBadResolution(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	res := tools.ImageToVideo{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, []byte(`{"image":"`+pngDataURL+`","resolution_name":"1080p"}`))
	if !res.IsError || !strings.Contains(res.Content, "resolution_name") {
		t.Fatalf("%s", res.Content)
	}
}

func TestImageToVideoRequiresImage(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	res := tools.ImageToVideo{}.Execute(context.Background(), tools.Context{SessionDir: t.TempDir()}, []byte(`{"prompt":"x"}`))
	if !res.IsError || !strings.Contains(strings.ToLower(res.Content), "image") {
		t.Fatalf("%s", res.Content)
	}
}

func TestImageToVideoAcceptsDurationString(t *testing.T) {
	isolation(t)
	config.ResetHomeForTest()
	session := t.TempDir()
	srv, probe := startVideoAPI(t, []byte("mp4"))
	args, _ := json.Marshal(map[string]any{"image": pngDataURL, "duration": "10"})
	res := tools.ImageToVideo{
		HTTP: srv.Client(), BaseURL: srv.URL, Bearer: "k", PollInterval: 0,
	}.Execute(context.Background(), tools.Context{SessionDir: session}, args)
	if res.IsError {
		t.Fatal(res.Content)
	}
	_, _, _, _, body := probe.snapshot()
	if body["duration"] != float64(10) {
		t.Fatalf("duration %+v", body["duration"])
	}
}
