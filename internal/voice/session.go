package voice

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// NoSpeechTimeout is how long a session may run without a transcript.
const NoSpeechTimeout = 10 * time.Second

// ErrNoSpeech is the Rust no-speech teardown copy.
const ErrNoSpeech = "No speech was detected. Voice stopped."

// MicFixHelp is the Linux mic-permission / device hint.
const MicFixHelp = "check the default input device and its volume in your sound settings (e.g. `pavucontrol`, or `wpctl status` on PipeWire)."

// EventKind is a pager-facing voice pipeline event.
type EventKind int

const (
	EventInterim EventKind = iota
	EventFinal
	EventError
)

// Event is mic → STT → pager.
type Event struct {
	Kind EventKind
	Text string
	Hint string
}

// Session is a live capture + streaming STT connection.
type Session struct {
	Events <-chan Event

	cancel context.CancelFunc
	cmd    *exec.Cmd
	conn   *websocket.Conn
	once   sync.Once
}

// Start opens the recorder and STT socket together, then streams PCM.
func Start(parent context.Context, cfg Config, bearer string) (*Session, error) {
	if strings.TrimSpace(bearer) == "" {
		return nil, fmt.Errorf("%s", ErrAuth)
	}
	rec, ok := DetectRecorder()
	if !ok {
		return nil, fmt.Errorf("%s", ErrNoRecorder)
	}
	ctx, cancel := context.WithCancel(parent)
	cmd, stdout, err := rec.Spawn(cfg.SampleRate)
	if err != nil {
		cancel()
		return nil, err
	}
	conn, err := dialSTT(cfg, bearer)
	if err != nil {
		cancel()
		stopRecorder(cmd, stdout)
		return nil, err
	}
	if err := waitReady(conn); err != nil {
		cancel()
		_ = conn.Close()
		stopRecorder(cmd, stdout)
		return nil, err
	}
	out := make(chan Event, 16)
	sess := &Session{Events: out, cancel: cancel, cmd: cmd, conn: conn}
	go sess.run(ctx, stdout, out)
	return sess, nil
}

// Stop ends capture, sends audio.done, and closes the socket.
func (s *Session) Stop() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
}

func (s *Session) run(ctx context.Context, stdout io.ReadCloser, out chan Event) {
	defer close(out)
	defer stopRecorder(s.cmd, stdout)
	defer func() {
		if s.conn != nil {
			_ = s.conn.WriteMessage(websocket.TextMessage, []byte(audioDoneJSON))
			_ = s.conn.Close()
		}
	}()

	go func() {
		buf := make([]byte, ReadChunk)
		for {
			n, err := stdout.Read(buf)
			if n > 0 && s.conn != nil {
				if werr := s.conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()

	deadline := time.Now().Add(NoSpeechTimeout)
	awaiting := true
	locked := ""
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if awaiting && time.Now().After(deadline) {
			out <- Event{Kind: EventError, Text: ErrNoSpeech, Hint: MicFixHelp}
			return
		}
		if s.conn == nil {
			return
		}
		if awaiting {
			_ = s.conn.SetReadDeadline(deadline)
		} else {
			_ = s.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		}
		kind, data, err := s.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if awaiting && time.Now().After(deadline) {
				out <- Event{Kind: EventError, Text: ErrNoSpeech, Hint: MicFixHelp}
				return
			}
			return
		}
		if kind != websocket.TextMessage {
			continue
		}
		ev, perr := parseServerEvent(data)
		if perr != nil {
			out <- Event{Kind: EventError, Text: perr.Error()}
			return
		}
		switch ev.Kind {
		case "partial":
			text := strings.TrimSpace(ev.Text)
			if text == "" {
				continue
			}
			awaiting = false
			if ev.SpeechFinal {
				locked = ""
				out <- Event{Kind: EventFinal, Text: ev.Text}
			} else if ev.IsFinal {
				if locked != "" {
					locked += " "
				}
				locked += text
				out <- Event{Kind: EventInterim, Text: locked}
			} else if locked == "" {
				out <- Event{Kind: EventInterim, Text: text}
			} else {
				out <- Event{Kind: EventInterim, Text: locked + " " + text}
			}
		case "done":
			locked = ""
			if strings.TrimSpace(ev.Text) != "" {
				awaiting = false
				out <- Event{Kind: EventFinal, Text: ev.Text}
			}
			return
		case "error":
			msg := ev.Message
			if msg == "" {
				msg = "STT error"
			}
			out <- Event{Kind: EventError, Text: msg}
			return
		case "ready", "unknown":
			continue
		}
	}
}

func stopRecorder(cmd *exec.Cmd, stdout io.ReadCloser) {
	if stdout != nil {
		_ = stdout.Close()
	}
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}
