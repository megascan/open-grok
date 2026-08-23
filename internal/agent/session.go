package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/codemode"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/workspace"
)

// Session is one persisted conversation.
type Session struct {
	ID             string                `json:"id"`
	Title          string                `json:"title,omitempty"`
	CWD            string                `json:"cwd"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
	ModelID        string                `json:"model_id"`
	Provider       types.Provider        `json:"provider"`
	Backend        types.ApiBackend      `json:"backend"`
	Messages       []types.Message       `json:"messages"`
	EverUsedNonXAI bool                  `json:"ever_used_codex"`
	Dir            string                `json:"-"`
	Plan           workspace.PlanTracker `json:"-"`
	YOLO           bool                  `json:"-"`
	Allow          []string              `json:"-"`
	Deny           []string              `json:"-"`
	JS             *codemode.Runtime     `json:"-"`
	SubagentDepth  int                   `json:"-"`
}

// NewSession creates a session directory under ~/.opengrok/sessions/<cwd>/<id>.
func NewSession(cwd string, model catalog.Model, yolo bool) (*Session, error) {
	id := uuid.NewString()
	root, err := config.EnsureSessionsCWDDirIn(config.Home(), cwd)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Session{
		ID:        id,
		CWD:       cwd,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		ModelID:   model.ID,
		Provider:  model.Provider,
		Backend:   model.APIBackend,
		Dir:       dir,
		YOLO:      yolo,
		Plan:      workspace.NewInactive(),
	}
	s.Plan.PlanFile = filepath.Join(dir, "plan.md")
	if !model.Provider.AllowsXAIServices() {
		s.EverUsedNonXAI = true
	}
	return s, s.Persist()
}

// LoadSession reads an existing session by id under the cwd encoding.
func LoadSession(cwd, id string) (*Session, error) {
	root := config.SessionsCWDDirIn(config.Home(), cwd)
	dir := filepath.Join(root, id)
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	s.Dir = dir
	s.Plan.PlanFile = filepath.Join(dir, "plan.md")
	return &s, nil
}

// ListSessions returns sessions for cwd, newest first.
func ListSessions(cwd string) ([]Session, error) {
	root := config.SessionsCWDDirIn(config.Home(), cwd)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if !e.IsDir() || e.Name() == ".cwd" {
			continue
		}
		s, err := LoadSession(cwd, e.Name())
		if err != nil {
			continue
		}
		out = append(out, *s)
	}
	// newest first
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].UpdatedAt.After(out[i].UpdatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// Persist writes session.json, summary.json, and appends updates.jsonl.
func (s *Session) Persist() error {
	s.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "session.json"), data, 0o600); err != nil {
		return err
	}
	summary := map[string]any{
		"id":         s.ID,
		"title":      s.Title,
		"cwd":        s.CWD,
		"model_id":   s.ModelID,
		"provider":   s.Provider,
		"updated_at": s.UpdatedAt,
	}
	sum, _ := json.MarshalIndent(summary, "", "  ")
	return os.WriteFile(filepath.Join(s.Dir, "summary.json"), sum, 0o600)
}

// AppendUpdate writes one JSONL event for resume/forensics.
func (s *Session) AppendUpdate(event any) error {
	f, err := os.OpenFile(filepath.Join(s.Dir, "updates.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(event)
}

// CloseExportBoundary marks the session as having used a non-xAI provider.
func (s *Session) CloseExportBoundary(p types.Provider) {
	if !p.AllowsXAIServices() {
		s.EverUsedNonXAI = true
	}
}
