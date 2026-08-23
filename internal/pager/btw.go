package pager

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/sampler"
	"github.com/megascan/open-grok/internal/types"
)

type btwPhase int

const (
	btwLoading btwPhase = iota
	btwDone
	btwError
)

type btwState struct {
	Question string
	Answer   string
	Err      string
	Phase    btwPhase
	Scroll   int
}

func (m *model) openBtwComposer(prefill string) {
	m.btwDraft = prefill
	m.overlay = overlayBtw
}

func (m *model) renderBtwComposer(w int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("/btw") + "\n")
	b.WriteString(dimStyle.Render("Ask a side question without interrupting the running turn.") + "\n\n")
	b.WriteString(m.btwDraft + "█\n\n")
	b.WriteString(dimStyle.Render("enter send  ·  esc discard"))
	return modalBox.Width(w).Render(b.String())
}

func (m *model) handleBtwComposerKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.overlay = overlayNone
		m.btwDraft = ""
		return nil
	case tea.KeyEnter:
		q := strings.TrimSpace(m.btwDraft)
		m.overlay = overlayNone
		m.btwDraft = ""
		if q == "" {
			return nil
		}
		return m.startBTW(q)
	case tea.KeyBackspace:
		if len(m.btwDraft) > 0 {
			m.btwDraft = m.btwDraft[:len(m.btwDraft)-1]
		}
		return nil
	}
	if msg.Type == tea.KeyRunes {
		m.btwDraft += string(msg.Runes)
	}
	return nil
}

func (m *model) handleBtwPanelKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.btw == nil {
		return nil, false
	}
	switch msg.String() {
	case "esc":
		m.dismissBTW()
		return nil, true
	case "up", "k":
		if m.btw.Scroll > 0 {
			m.btw.Scroll--
		}
		return nil, true
	case "down", "j":
		m.btw.Scroll++
		return nil, true
	}
	return nil, false
}

func (m *model) dismissBTW() {
	if m.btw == nil {
		return
	}
	body := m.btw.Answer
	if m.btw.Phase == btwError {
		body = m.btw.Err
	}
	if strings.TrimSpace(body) == "" && m.btw.Phase == btwLoading {
		body = "(dismissed while loading)"
	}
	m.appendBlock(block{kind: blockBtw, title: m.btw.Question, body: body})
	m.btw = nil
	m.refreshVP()
}

func (m *model) startBTW(question string) tea.Cmd {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil
	}
	if m.sess == nil {
		m.appendSys("No session — start a chat first, then /btw.")
		return nil
	}
	m.btw = &btwState{Question: question, Phase: btwLoading}
	sess := m.sess
	model := m.opts.Model
	kimiCode := model.EnvKey == "KIMI_CODE_API_KEY"
	msgs := append([]types.Message(nil), sess.Messages...)
	msgs = append(msgs, types.Message{
		Role:    types.RoleUser,
		Content: "BTW (answer this aside separately; do not continue or interrupt the main task):\n" + question,
	})
	return func() tea.Msg {
		bearer, _, err := auth.ResolveBearer(model.Provider, auth.EnvKeys(model.Provider), kimiCode)
		if err != nil {
			return btwDoneMsg{err: err}
		}
		id := model.Model
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
			Messages:  msgs,
			SessionID: sess.ID + "-btw",
		})
		if err != nil {
			return btwDoneMsg{err: err}
		}
		prog := m.prog
		for ev := range ch {
			switch ev.Kind {
			case types.StreamDelta:
				if ev.Text != "" && prog != nil {
					prog.Send(btwDeltaMsg{text: ev.Text})
				}
			case types.StreamError:
				return btwDoneMsg{err: ev.Err}
			}
		}
		return btwDoneMsg{}
	}
}

func (m *model) renderBtwPanel() string {
	if m.btw == nil {
		return ""
	}
	w := max(24, m.width-2)
	var b strings.Builder
	b.WriteString(titleStyle.Render("/btw "+truncate(m.btw.Question, max(12, w-12))) + "\n")
	switch m.btw.Phase {
	case btwLoading:
		body := m.btw.Answer
		if strings.TrimSpace(body) == "" {
			body = m.spin.View() + " thinking…"
		}
		b.WriteString(dimStyle.Render(truncate(body, 800)))
	case btwError:
		b.WriteString(errStyle.Render(m.btw.Err))
	default:
		lines := strings.Split(m.btw.Answer, "\n")
		start := m.btw.Scroll
		if start >= len(lines) {
			start = max(0, len(lines)-1)
			m.btw.Scroll = start
		}
		end := start + 8
		if end > len(lines) {
			end = len(lines)
		}
		b.WriteString(strings.Join(lines[start:end], "\n"))
	}
	b.WriteString("\n" + dimStyle.Render("esc persist to scrollback  ·  j/k scroll"))
	return inputBox.Width(w).Render(b.String())
}

func (m *model) renderBtwBlock(bl block, w int) string {
	inner := toolStyle.Render("BTW") + " " + dimStyle.Render("/btw "+truncate(bl.title, 40)) + "\n" + renderMarkdown(bl.body, w)
	return toolCard.Width(w - 2).Render(inner)
}
