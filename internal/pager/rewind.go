package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/types"
)

type rewindPhase int

const (
	rewindPicker rewindPhase = iota
	rewindConfirm
	rewindCancelOffer
)

type rewindPoint struct {
	MsgIndex int
	Preview  string
}

func (m *model) openRewindPicker() {
	if m.sess == nil {
		m.appendSys("no session to rewind")
		return
	}
	m.rewindPoints = collectRewindPoints(m.sess.Messages)
	if len(m.rewindPoints) == 0 {
		m.appendSys("nothing to rewind")
		return
	}
	m.rewindCursor = 0
	m.rewindTarget = -1
	m.rewindPreview = ""
	if m.busy {
		m.rewindPhase = rewindCancelOffer
	} else {
		m.rewindPhase = rewindPicker
	}
	m.overlay = overlayRewind
}

func collectRewindPoints(msgs []types.Message) []rewindPoint {
	var out []rewindPoint
	for i, msg := range msgs {
		if msg.Role != types.RoleUser {
			continue
		}
		preview := strings.TrimSpace(msg.Content)
		if preview == "" {
			preview = "(no preview)"
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		out = append(out, rewindPoint{MsgIndex: i, Preview: preview})
	}
	return out
}

func (m *model) renderRewind(w int) string {
	var b strings.Builder
	switch m.rewindPhase {
	case rewindCancelOffer:
		writeAccent(&b, titleStyle.Render("A turn is currently running."))
		writeAccent(&b, dimStyle.Render("Would you like to cancel it before rewinding?"))
		writeAccent(&b, "")
		writeAccent(&b, radioRow("y", "Cancel turn and rewind", m.rewindCursor == 0))
		writeAccent(&b, radioRow("n", "Let it finish", m.rewindCursor == 1))
	case rewindConfirm:
		preview := m.rewindPreview
		if preview == "" {
			preview = "this turn"
		}
		writeAccent(&b, titleStyle.Render("Rewind conversation to “"+truncate(preview, max(12, w-28))+"”?"))
		writeAccent(&b, "")
		writeAccent(&b, radioRow("y", "Yes", m.rewindCursor == 0))
		writeAccent(&b, radioRow("a", "Yes, and don't ask again", m.rewindCursor == 1))
		writeAccent(&b, radioRow("n", "No", m.rewindCursor == 2))
	default:
		writeAccent(&b, titleStyle.Render("Rewind to which turn?"))
		writeAccent(&b, "")
		if len(m.rewindPoints) == 0 {
			writeAccent(&b, dimStyle.Render("(empty)"))
			break
		}
		start := 0
		limit := 12
		if m.rewindCursor >= limit {
			start = m.rewindCursor - limit + 1
		}
		for i, p := range m.rewindPoints {
			if i < start || i >= start+limit {
				continue
			}
			line := "\u00b7 " + truncate(p.Preview, max(20, w-8))
			if i == m.rewindCursor {
				line = selStyle.Render(line)
			}
			writeAccent(&b, line)
		}
		writeAccent(&b, "")
		writeAccent(&b, dimStyle.Render("j/k  ·  enter  ·  esc"))
	}
	return modalBox.Width(w).Render(b.String())
}

func (m *model) handleRewindKey(msg tea.KeyMsg) tea.Cmd {
	s := msg.String()
	switch m.rewindPhase {
	case rewindCancelOffer:
		switch s {
		case "up", "k":
			m.rewindCursor = 0
		case "down", "j":
			m.rewindCursor = 1
		case "y":
			m.rewindCursor = 0
			return m.applyRewindChoice()
		case "n", "esc":
			m.overlay = overlayNone
			return nil
		case "enter":
			return m.applyRewindChoice()
		}
	case rewindConfirm:
		switch s {
		case "up", "k":
			if m.rewindCursor > 0 {
				m.rewindCursor--
			}
		case "down", "j":
			if m.rewindCursor < 2 {
				m.rewindCursor++
			}
		case "y":
			m.rewindCursor = 0
			return m.applyRewindChoice()
		case "a":
			m.rewindCursor = 1
			return m.applyRewindChoice()
		case "n", "esc":
			m.overlay = overlayNone
			return nil
		case "enter":
			return m.applyRewindChoice()
		}
	default:
		switch s {
		case "up", "k":
			if m.rewindCursor > 0 {
				m.rewindCursor--
			}
		case "down", "j":
			if m.rewindCursor+1 < len(m.rewindPoints) {
				m.rewindCursor++
			}
		case "esc":
			m.overlay = overlayNone
		case "enter":
			return m.applyRewindChoice()
		}
	}
	return nil
}

func (m *model) applyRewindChoice() tea.Cmd {
	switch m.rewindPhase {
	case rewindCancelOffer:
		if m.rewindCursor != 0 {
			m.overlay = overlayNone
			return nil
		}
		if m.turnCancel != nil {
			m.turnCancel()
		}
		m.rewindPhase = rewindPicker
		m.rewindCursor = 0
		return nil
	case rewindConfirm:
		switch m.rewindCursor {
		case 1:
			m.cfg.UI.ConfirmRewind = false
			m.persistUI()
			fallthrough
		case 0:
			m.executeRewind(m.rewindTarget, m.rewindPreview)
		default:
			m.overlay = overlayNone
		}
		return nil
	default:
		if m.rewindCursor >= len(m.rewindPoints) {
			return nil
		}
		p := m.rewindPoints[m.rewindCursor]
		if m.cfg.UI.ConfirmRewind {
			m.rewindPhase = rewindConfirm
			m.rewindTarget = p.MsgIndex
			m.rewindPreview = p.Preview
			m.rewindCursor = 0
			return nil
		}
		m.executeRewind(p.MsgIndex, p.Preview)
		return nil
	}
}

func (m *model) executeRewind(msgIndex int, preview string) {
	if m.sess == nil || msgIndex < 0 || msgIndex >= len(m.sess.Messages) {
		m.overlay = overlayNone
		return
	}
	text := m.sess.Messages[msgIndex].Content
	m.sess.Messages = m.sess.Messages[:msgIndex]
	_ = m.sess.Persist()
	m.replaySessionBlocks()
	m.ta.SetValue(strings.TrimSpace(text))
	m.ta.Focus()
	m.focusScroll = false
	m.overlay = overlayNone
	m.appendSys("rewound to “" + truncate(preview, 48) + "”")
}

func (m *model) doRewind() {
	if m.sess == nil {
		return
	}
	pts := collectRewindPoints(m.sess.Messages)
	if len(pts) == 0 {
		return
	}
	last := pts[len(pts)-1]
	m.executeRewind(last.MsgIndex, last.Preview)
}

func (m *model) replaySessionBlocks() {
	var header []block
	for _, bl := range m.blocks {
		if bl.kind == blockSystem && strings.HasPrefix(bl.body, "Entropy ") {
			header = append(header, bl)
			if len(header) >= 2 {
				break
			}
		}
	}
	m.blocks = header
	if m.sess == nil {
		return
	}
	for _, msg := range m.sess.Messages {
		switch msg.Role {
		case types.RoleUser:
			m.appendBlock(block{kind: blockUser, body: msg.Content})
		case types.RoleAssistant:
			m.appendBlock(block{kind: blockAgent, body: msg.Content})
		case types.RoleTool:
			m.appendBlock(block{kind: blockTool, title: msg.Name, body: ""})
			m.appendBlock(block{kind: blockToolResult, title: msg.Name, body: msg.Content})
		}
	}
	m.rebuildHistory()
	m.refreshVP()
}
