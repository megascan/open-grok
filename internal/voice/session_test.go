package voice

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestStartSessionFakeMicAndSTT(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "pw-record")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec /bin/cat /dev/zero\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	orig := probePATH
	t.Cleanup(func() { probePATH = orig })
	probePATH = executableOnPATH

	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.created"}`))
		for {
			kind, _, err := c.ReadMessage()
			if err != nil {
				return
			}
			if kind == websocket.BinaryMessage {
				_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.partial","text":"hello world","is_final":true,"speech_final":true}`))
				return
			}
		}
	}))
	defer s.Close()

	cfg := DefaultConfig()
	cfg.WSURL = "ws" + strings.TrimPrefix(s.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := Start(ctx, cfg, "token")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()
	select {
	case ev := <-sess.Events:
		if ev.Kind != EventFinal || ev.Text != "hello world" {
			t.Fatalf("%+v", ev)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for final")
	}
}

func TestStartRequiresBearer(t *testing.T) {
	_, err := Start(context.Background(), DefaultConfig(), "")
	if err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("%v", err)
	}
}

func TestStartRequiresRecorder(t *testing.T) {
	orig := probePATH
	t.Cleanup(func() { probePATH = orig })
	probePATH = func(string) bool { return false }
	_, err := Start(context.Background(), DefaultConfig(), "tok")
	if err == nil || !strings.Contains(err.Error(), "pw-record") {
		t.Fatalf("%v", err)
	}
}
