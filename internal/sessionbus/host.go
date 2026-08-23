// Package sessionbus is the machine-local cross-process session bus.
//
// Live root sessions publish presence under $OPENGROK_HOME/session-bus/ and
// message each other over a per-process IPC socket. There is no hub daemon.
// Peer bodies are untrusted model input, never user consent.
package sessionbus

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/megascan/open-grok/internal/config"
)

const (
	protocolVersion = 1
	staleTTL        = 20 * time.Second
	heartbeatEvery  = 5 * time.Second
	maxBodyBytes    = 32 * 1024
)

// SessionInfo is one announced root session.
type SessionInfo struct {
	SessionID   string  `json:"session_id"`
	CWD         string  `json:"cwd"`
	ProjectName string  `json:"project_name"`
	ModelID     string  `json:"model_id"`
	Title       *string `json:"title"`
	Status      string  `json:"status"`
	UpdatedAtMS int64   `json:"updated_at_ms"`
	Conflict    bool    `json:"conflict,omitempty"`
}

// Presence is one process's published roster.
type Presence struct {
	InstanceID      string        `json:"instance_id"`
	PID             int           `json:"pid"`
	SocketPath      string        `json:"socket_path"`
	ProtocolVersion int           `json:"protocol_version"`
	HeartbeatAtMS   int64         `json:"heartbeat_at_ms"`
	StartedAtMS     int64         `json:"started_at_ms"`
	Sessions        []SessionInfo `json:"sessions"`
}

// Delivered is an inbound peer message queued for the recipient model.
type Delivered struct {
	MessageID     string
	TargetSession string
	SourceSession string
	SourceProject string
	Body          string
}

// InboxFunc receives a peer message for a hosted session.
type InboxFunc func(d Delivered) string // returns Ack status

// Host is the per-process bus endpoint.
type Host struct {
	mu       sync.Mutex
	id       string
	dir      string
	path     string
	sock     string
	ln       net.Listener
	sessions map[string]SessionInfo
	inbox    InboxFunc
	cancel   context.CancelFunc
}

var (
	processMu sync.Mutex
	process   *Host
)

// Start returns the process-wide host, creating it on first use.
func Start(inbox InboxFunc) (*Host, error) {
	processMu.Lock()
	defer processMu.Unlock()
	dir := filepath.Join(config.Home(), "session-bus")
	if process != nil && process.dir == dir {
		if inbox != nil {
			process.mu.Lock()
			process.inbox = inbox
			process.mu.Unlock()
		}
		return process, nil
	}
	if process != nil {
		stopHost(process)
		process = nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	id := fmt.Sprintf("p%d-%s", os.Getpid(), uuid.NewString()[:8])
	h := &Host{
		id:       id,
		dir:      dir,
		path:     filepath.Join(dir, id+".json"),
		sock:     filepath.Join(dir, id+".sock"),
		sessions: map[string]SessionInfo{},
		inbox:    inbox,
	}
	ln, sockPath, err := listenBus(h.sock)
	if err != nil {
		return nil, err
	}
	h.ln = ln
	h.sock = sockPath
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go h.serve()
	go h.heartbeat(ctx)
	process = h
	return h, h.publish()
}

// Stop shuts the process host down. Tests call this; the product binary
// leaves it running for the process lifetime.
func Stop() {
	processMu.Lock()
	h := process
	process = nil
	processMu.Unlock()
	stopHost(h)
}

func stopHost(h *Host) {
	if h == nil {
		return
	}
	if h.cancel != nil {
		h.cancel()
	}
	if h.ln != nil {
		_ = h.ln.Close()
	}
	_ = os.Remove(h.path)
	_ = os.Remove(h.sock)
	_ = os.Remove(h.sock + ".port")
}

// Register announces a root session. Subagents must not call this.
func (h *Host) Register(info SessionInfo) {
	if h == nil {
		return
	}
	h.mu.Lock()
	info.UpdatedAtMS = time.Now().UnixMilli()
	if info.Status == "" {
		info.Status = "idle"
	}
	h.sessions[info.SessionID] = info
	h.mu.Unlock()
	_ = h.publish()
}

// Unregister drops a session. The presence file is removed when none remain.
func (h *Host) Unregister(sessionID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	delete(h.sessions, sessionID)
	empty := len(h.sessions) == 0
	h.mu.Unlock()
	if empty {
		_ = os.Remove(h.path)
		return
	}
	_ = h.publish()
}

// SetStatus updates busy/idle for a hosted session.
func (h *Host) SetStatus(sessionID, status string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if s, ok := h.sessions[sessionID]; ok {
		s.Status = status
		s.UpdatedAtMS = time.Now().UnixMilli()
		h.sessions[sessionID] = s
	}
	h.mu.Unlock()
	_ = h.publish()
}

func (h *Host) publish() error {
	h.mu.Lock()
	p := Presence{
		InstanceID:      h.id,
		PID:             os.Getpid(),
		SocketPath:      h.sock,
		ProtocolVersion: protocolVersion,
		HeartbeatAtMS:   time.Now().UnixMilli(),
		StartedAtMS:     h.startedMS(),
	}
	for _, s := range h.sessions {
		p.Sessions = append(p.Sessions, s)
	}
	path := h.path
	h.mu.Unlock()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (h *Host) startedMS() int64 {
	// Presence file mtime is a fine enough start if we have not stored one.
	return time.Now().UnixMilli()
}

func (h *Host) heartbeat(ctx context.Context) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = h.publish()
			n++
			if n%4 == 0 {
				gcStale(h.dir)
			}
		}
	}
}

func (h *Host) serve() {
	for {
		conn, err := h.ln.Accept()
		if err != nil {
			return
		}
		go h.handleConn(conn)
	}
}

func (h *Host) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	if !sc.Scan() {
		return
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
		return
	}
	enc := json.NewEncoder(conn)
	if _, ok := req["Ping"]; ok {
		_ = enc.Encode(map[string]any{"Pong": map[string]any{}})
		return
	}
	raw, ok := req["Message"]
	if !ok {
		return
	}
	var msg struct {
		V             int    `json:"v"`
		MessageID     string `json:"message_id"`
		Target        string `json:"target_session"`
		SourceSession string `json:"source_session"`
		SourceProject string `json:"source_project"`
		Body          string `json:"body"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	status := "Rejected"
	if msg.V == protocolVersion && len(msg.Body) <= maxBodyBytes {
		h.mu.Lock()
		_, hosted := h.sessions[msg.Target]
		inbox := h.inbox
		h.mu.Unlock()
		if !hosted {
			status = "UnknownSession"
		} else if inbox != nil {
			status = inbox(Delivered{
				MessageID:     msg.MessageID,
				TargetSession: msg.Target,
				SourceSession: msg.SourceSession,
				SourceProject: msg.SourceProject,
				Body:          msg.Body,
			})
			if status == "" {
				status = "Accepted"
			}
		} else {
			status = "Accepted"
		}
	}
	_ = enc.Encode(map[string]any{"Ack": map[string]any{"message_id": msg.MessageID, "status": status}})
}

// LiveSessions scans presence files. Stale files and dead PIDs are dropped.
func LiveSessions() []SessionInfo {
	dir := filepath.Join(config.Home(), "session-bus")
	gcStale(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	seen := map[string]SessionInfo{}
	conflict := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p Presence
		if json.Unmarshal(data, &p) != nil {
			continue
		}
		if time.Since(time.UnixMilli(p.HeartbeatAtMS)) > staleTTL {
			continue
		}
		for _, s := range p.Sessions {
			if prev, ok := seen[s.SessionID]; ok {
				conflict[s.SessionID] = true
				if s.UpdatedAtMS < prev.UpdatedAtMS {
					continue
				}
			}
			seen[s.SessionID] = s
		}
	}
	var out []SessionInfo
	for id, s := range seen {
		s.Conflict = conflict[id]
		out = append(out, s)
	}
	return out
}

// Find returns a live session by id.
func Find(id string) (SessionInfo, Presence, bool) {
	dir := filepath.Join(config.Home(), "session-bus")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return SessionInfo{}, Presence{}, false
	}
	var best SessionInfo
	var bestP Presence
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p Presence
		if json.Unmarshal(data, &p) != nil {
			continue
		}
		if time.Since(time.UnixMilli(p.HeartbeatAtMS)) > staleTTL {
			continue
		}
		for _, s := range p.Sessions {
			if s.SessionID == id && (!found || s.UpdatedAtMS >= best.UpdatedAtMS) {
				best, bestP, found = s, p, true
			}
		}
	}
	return best, bestP, found
}

// Send delivers a message to a live peer session.
func Send(target, sourceSession, sourceProject, body string) (string, error) {
	if len(body) > maxBodyBytes {
		return "Rejected", fmt.Errorf("message exceeds %d bytes", maxBodyBytes)
	}
	info, p, ok := Find(target)
	if !ok {
		return "UnknownSession", fmt.Errorf("session %s is not live on the bus", target)
	}
	_ = info
	conn, err := dialBus(p.SocketPath)
	if err != nil {
		return "Rejected", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	req := map[string]any{
		"Message": map[string]any{
			"v":              protocolVersion,
			"message_id":     uuid.NewString(),
			"target_session": target,
			"source_session": sourceSession,
			"source_project": sourceProject,
			"body":           body,
		},
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return "Rejected", err
	}
	var resp struct {
		Ack struct {
			Status string `json:"status"`
		} `json:"Ack"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return "Rejected", err
	}
	if resp.Ack.Status == "" {
		return "Rejected", fmt.Errorf("empty ack")
	}
	return resp.Ack.Status, nil
}

func gcStale(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var p Presence
		if json.Unmarshal(data, &p) != nil {
			_ = os.Remove(path)
			continue
		}
		stale := now.Sub(time.UnixMilli(p.HeartbeatAtMS)) > staleTTL
		dead := p.PID > 0 && !pidAlive(p.PID)
		if stale || dead {
			_ = os.Remove(path)
			_ = os.Remove(strings.TrimSuffix(path, ".json") + ".sock")
			_ = os.Remove(strings.TrimSuffix(path, ".json") + ".sock.port")
		}
	}
}

func projectName(cwd string) string {
	base := filepath.Base(cwd)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "workspace"
	}
	return base
}

// ProjectName is the final cwd component used on the bus roster.
func ProjectName(cwd string) string { return projectName(cwd) }
