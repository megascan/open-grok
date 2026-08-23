package pager

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/sampler"
	"github.com/megascan/open-grok/internal/types"
)

type recapDoneMsg struct {
	body string
	err  error
}

func recapEnabled(cfg config.File) bool {
	if config.EnvIsOff("ENTROPY_SESSION_RECAP", "GROK_SESSION_RECAP") {
		return false
	}
	if cfg.Features.SessionRecap != nil {
		return *cfg.Features.SessionRecap
	}
	return true
}

func (m *model) sendRecap() tea.Cmd {
	if !recapEnabled(m.cfg) {
		m.status = agent.RecapDisabledNotice()
		return nil
	}
	if m.sess == nil {
		m.status = agent.NoSessionNotice()
		return nil
	}
	hasUser := agent.CountUserMessages(m.sess.Messages) > 0
	if !hasUser {
		m.status = agent.RecapUnavailableToast(false)
		return nil
	}
	m.ta.Reset()
	m.status = "recap"
	m.appendBlock(block{kind: blockSystem, body: "Recap — generating…"})
	m.refreshVP()
	sess := m.sess
	model := m.opts.Model
	kimiCode := model.EnvKey == "KIMI_CODE_API_KEY"
	snap := agent.SnapshotForRecap(sess.Messages)
	recapModel := m.cfg.UI.RecapModel
	return func() tea.Msg {
		bearer, _, err := auth.ResolveBearer(model.Provider, auth.EnvKeys(model.Provider), kimiCode)
		if err != nil {
			return recapDoneMsg{err: err}
		}
		id := recapModel
		if id == "" {
			id = model.Model
		}
		if id == "" {
			id = model.ID
		}
		ad := sampler.Adapter{
			Provider: model.Provider,
			Backend:  model.APIBackend,
			BaseURL:  strings.TrimRight(model.BaseURL, "/"),
			Bearer:   bearer,
		}
		ch, err := ad.Stream(context.Background(), types.SampleRequest{
			Model:     id,
			Messages:  snap,
			SessionID: sess.ID + "-recap",
		})
		if err != nil {
			return recapDoneMsg{err: err}
		}
		var raw strings.Builder
		for ev := range ch {
			switch ev.Kind {
			case types.StreamDelta:
				raw.WriteString(ev.Text)
			case types.StreamError:
				return recapDoneMsg{err: ev.Err}
			}
		}
		return recapDoneMsg{body: raw.String()}
	}
}

func (m *model) applyRecapDone(msg recapDoneMsg) {
	line := ""
	if msg.err != nil || strings.TrimSpace(msg.body) == "" {
		m.status = agent.RecapUnavailableToast(true)
		line = m.status
	} else {
		line = agent.FormatRecapLine(msg.body)
		if line == "" {
			m.status = agent.RecapUnavailableToast(true)
			line = m.status
		} else {
			m.status = "ready"
		}
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == blockSystem && strings.HasPrefix(m.blocks[i].body, "Recap —") {
			m.blocks[i].body = line
			m.refreshVP()
			return
		}
	}
	m.appendSys(line)
	m.refreshVP()
}
