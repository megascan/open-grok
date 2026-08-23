package voice

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	sttHandshakeTimeout = 15 * time.Second
	sttReadyTimeout     = 10 * time.Second
)

// ErrAuth is the Rust require_bearer copy.
const ErrAuth = "not signed in — run `open-grok login` or set XAI_API_KEY"

const audioDoneJSON = `{"type":"audio.done"}`

// serverEvent is a parsed STT websocket payload.
type serverEvent struct {
	Kind        string
	Text        string
	IsFinal     bool
	SpeechFinal bool
	Message     string
}

func parseServerEvent(raw []byte) (serverEvent, error) {
	var wire struct {
		Type        string  `json:"type"`
		Text        string  `json:"text"`
		IsFinal     bool    `json:"is_final"`
		SpeechFinal bool    `json:"speech_final"`
		Message     string  `json:"message"`
		Duration    float32 `json:"duration"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return serverEvent{}, fmt.Errorf("parse error: %w", err)
	}
	switch wire.Type {
	case "transcript.created":
		return serverEvent{Kind: "ready"}, nil
	case "transcript.partial":
		return serverEvent{Kind: "partial", Text: wire.Text, IsFinal: wire.IsFinal, SpeechFinal: wire.SpeechFinal}, nil
	case "transcript.done":
		return serverEvent{Kind: "done", Text: wire.Text}, nil
	case "error":
		return serverEvent{Kind: "error", Message: wire.Message}, nil
	default:
		return serverEvent{Kind: "unknown"}, nil
	}
}

func sttHeaders(cfg Config, bearer string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+bearer)
	if id := strings.TrimSpace(cfg.ClientIdentifier); id != "" && strings.IndexByte(id, '\n') < 0 {
		h.Set("x-grok-client-identifier", id)
	}
	if ua := strings.TrimSpace(cfg.UserAgent); ua != "" && strings.IndexByte(ua, '\n') < 0 {
		h.Set("User-Agent", ua)
	}
	return h
}

func dialSTT(cfg Config, bearer string) (*websocket.Conn, error) {
	if strings.TrimSpace(bearer) == "" {
		return nil, fmt.Errorf("%s", ErrAuth)
	}
	raw, err := cfg.STTWSURLWithQuery()
	if err != nil {
		return nil, err
	}
	dialer := websocket.Dialer{HandshakeTimeout: sttHandshakeTimeout}
	conn, _, err := dialer.Dial(raw, sttHeaders(cfg, bearer))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return conn, nil
}

func waitReady(conn *websocket.Conn) error {
	deadline := time.Now().Add(sttReadyTimeout)
	_ = conn.SetReadDeadline(deadline)
	defer conn.SetReadDeadline(time.Time{})
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("timed out waiting for transcript.created")
		}
		if kind != websocket.TextMessage {
			continue
		}
		ev, err := parseServerEvent(data)
		if err != nil {
			return err
		}
		switch ev.Kind {
		case "ready":
			return nil
		case "error":
			if ev.Message == "" {
				ev.Message = "STT error"
			}
			return fmt.Errorf("%s", ev.Message)
		case "unknown":
			continue
		default:
			return fmt.Errorf("unexpected event before ready")
		}
	}
}
