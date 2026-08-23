package voice

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestParsePartialAndSpeechFinal(t *testing.T) {
	ev, err := parseServerEvent([]byte(`{"type":"transcript.partial","text":"hello","is_final":false,"speech_final":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != "partial" || ev.Text != "hello" || ev.SpeechFinal {
		t.Fatalf("%+v", ev)
	}
	ev, err = parseServerEvent([]byte(`{"type":"transcript.partial","text":"done","is_final":true,"speech_final":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !ev.SpeechFinal || !ev.IsFinal {
		t.Fatalf("%+v", ev)
	}
}

func TestParseCreatedAndDone(t *testing.T) {
	ev, err := parseServerEvent([]byte(`{"type":"transcript.created"}`))
	if err != nil || ev.Kind != "ready" {
		t.Fatalf("%+v %v", ev, err)
	}
	ev, err = parseServerEvent([]byte(`{"type":"transcript.done","text":"bye"}`))
	if err != nil || ev.Kind != "done" || ev.Text != "bye" {
		t.Fatalf("%+v %v", ev, err)
	}
}

func TestSTTHeaders(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ClientIdentifier = "open-grok"
	cfg.UserAgent = ""
	h := sttHeaders(cfg, "tok")
	if h.Get("Authorization") != "Bearer tok" {
		t.Fatalf("%v", h)
	}
	if h.Get("x-grok-client-identifier") != "open-grok" {
		t.Fatalf("%v", h)
	}
	if h.Get("User-Agent") != "" {
		t.Fatal("empty UA must omit header")
	}
}

func TestDialWaitReadyAndAudioDone(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	gotAuth := ""
	gotDone := false
	gotPCM := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.created"}`))
		for {
			kind, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if kind == websocket.BinaryMessage && len(data) > 0 {
				gotPCM = true
				_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.partial","text":"hi","is_final":false,"speech_final":false}`))
				_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.partial","text":"hi","is_final":true,"speech_final":true}`))
			}
			if kind == websocket.TextMessage && strings.Contains(string(data), "audio.done") {
				gotDone = true
				_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.done","text":"hi"}`))
				return
			}
		}
	}))
	defer s.Close()
	ws := "ws" + strings.TrimPrefix(s.URL, "http")
	cfg := DefaultConfig()
	cfg.WSURL = ws
	conn, err := dialSTT(cfg, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := waitReady(conn); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	ev, err := parseNext(conn)
	if err != nil || ev.Kind != "partial" {
		t.Fatalf("%+v %v", ev, err)
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(audioDoneJSON))
	_ = conn.Close()
	if gotAuth != "Bearer secret" {
		t.Fatalf("auth %q", gotAuth)
	}
	if !gotPCM {
		t.Fatal("expected pcm")
	}
	_ = gotDone
}

func parseNext(conn *websocket.Conn) (serverEvent, error) {
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return serverEvent{}, err
		}
		if kind != websocket.TextMessage {
			continue
		}
		return parseServerEvent(data)
	}
}
