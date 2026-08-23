package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const emptyPlanPlaceholder = `# No plan written yet

The agent exited plan mode without writing a plan.

- **Approve** — leave plan mode and start implementing
- **Request changes** — send the agent back to planning
- **Quit** — abandon and turn plan mode off
`

func (m *model) renderPlanApproval(w int) string {
	if m.plan == nil {
		return ""
	}
	var b strings.Builder
	title := "Plan approval"
	body := m.plan.Plan
	if !m.plan.HasPlan || strings.TrimSpace(body) == "" {
		title = "No plan written — approve or request changes"
		body = emptyPlanPlaceholder
	}
	b.WriteString(titleStyle.Render(title) + "\n\n")
	lines := strings.Split(body, "\n")
	start := m.planScroll
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		start = max(0, len(lines)-1)
	}
	end := min(len(lines), start+12)
	for i := start; i < end; i++ {
		mark := "  "
		if m.planFocus == "preview" && i == start {
			mark = "▸ "
		}
		b.WriteString(mark + lines[i] + "\n")
	}
	if m.planFocus == "prompt" || m.planFocus == "comment" {
		label := "revision notes: "
		if m.planFocus == "comment" {
			label = "comment: "
		}
		b.WriteString("\n" + claimStyle.Render(label) + m.planDraft + "█\n")
	}
	b.WriteString("\n" + dimStyle.Render("a approve  ·  s request changes  ·  c comment  ·  y copy  ·  q quit  ·  tab prompt"))
	return modalBox.Width(w).Render(b.String())
}

func (m *model) handlePlanKey(msg tea.KeyMsg) tea.Cmd {
	if m.plan == nil {
		return nil
	}
	if m.planFocus == "prompt" || m.planFocus == "comment" {
		switch msg.Type {
		case tea.KeyEsc:
			m.planFocus = "preview"
			m.planDraft = ""
		case tea.KeyEnter:
			if m.planFocus == "comment" {
				m.planComments = append(m.planComments, m.planDraft)
				m.planDraft = ""
				m.planFocus = "preview"
				return nil
			}
			m.replyPlan("changes:" + strings.TrimSpace(m.planDraft))
		case tea.KeyBackspace:
			if len(m.planDraft) > 0 {
				m.planDraft = m.planDraft[:len(m.planDraft)-1]
			}
		case tea.KeyRunes:
			m.planDraft += string(msg.Runes)
		}
		return nil
	}
	switch msg.String() {
	case "a":
		out := "approve"
		if len(m.planComments) > 0 {
			out = "approve:" + strings.Join(m.planComments, "\n")
		}
		m.replyPlan(out)
	case "s":
		m.planFocus = "prompt"
	case "c":
		m.planFocus = "comment"
	case "y":
		text := m.plan.Plan
		if !m.plan.HasPlan || strings.TrimSpace(text) == "" {
			text = emptyPlanPlaceholder
		}
		return m.copyText(text, true)
	case "q":
		m.replyPlan("quit")
	case "tab":
		if m.planFocus == "preview" {
			m.planFocus = "prompt"
		} else {
			m.planFocus = "preview"
		}
	case "up", "k":
		if m.planScroll > 0 {
			m.planScroll--
		}
	case "down", "j":
		m.planScroll++
	case "esc":
		m.planFocus = "preview"
	}
	return nil
}

func (m *model) replyPlan(decision string) {
	if m.plan == nil {
		return
	}
	ch := m.plan.Reply
	m.plan = nil
	m.planDraft = ""
	m.planComments = nil
	m.planFocus = "preview"
	if m.overlay == overlayPlan {
		m.overlay = overlayNone
	}
	if ch != nil {
		ch <- decision
	}
}
