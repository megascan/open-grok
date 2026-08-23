package pager

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type cancelTurnState struct {
	Running int
}

var cancelTurnChoices = []string{
	"Stop running",
	"Continue to run",
	"Always stop",
	"Always continue",
}

func (m *model) renderCancelTurn(w int) string {
	if m.cancel == nil {
		return ""
	}
	n := m.cancel.Running
	title := "Subagents are still running. Stop them?"
	count := fmt.Sprintf("%d subagents running", n)
	if n == 1 {
		count = "1 subagent running"
	}
	var b strings.Builder
	writeAccent(&b, titleStyle.Render(title))
	writeAccent(&b, dimStyle.Render(count))
	writeAccent(&b, "")
	for i, c := range cancelTurnChoices {
		writeAccent(&b, radioRow(fmt.Sprintf("%d", i+1), c, i == m.cancelCursor))
	}
	writeAccent(&b, "")
	writeAccent(&b, dimStyle.Render("enter  ·  tab walk  ·  esc keep running"))
	return modalBox.Width(w).Render(b.String())
}

func (m *model) handleCancelTurnKey(msg tea.KeyMsg) tea.Cmd {
	if m.cancel == nil {
		return nil
	}
	n := len(cancelTurnChoices)
	switch msg.String() {
	case "up", "k", "shift+tab":
		if m.cancelCursor == 0 {
			m.cancelCursor = n - 1
		} else {
			m.cancelCursor--
		}
	case "down", "j", "tab":
		m.cancelCursor = (m.cancelCursor + 1) % n
	case "enter", " ":
		m.applyCancelChoice(cancelTurnChoices[m.cancelCursor])
	case "esc":
		m.applyCancelChoice("Continue to run")
	case "1":
		m.applyCancelChoice(cancelTurnChoices[0])
	case "2":
		m.applyCancelChoice(cancelTurnChoices[1])
	case "3":
		m.applyCancelChoice(cancelTurnChoices[2])
	case "4":
		m.applyCancelChoice(cancelTurnChoices[3])
	}
	return nil
}

func (m *model) applyCancelChoice(choice string) {
	m.cancel = nil
	if m.overlay == overlayCancelTurn {
		m.overlay = overlayNone
	}
	switch choice {
	case "Always stop":
		m.alwaysStopKids = true
		m.doCancelTurn()
	case "Stop running":
		m.doCancelTurn()
	case "Always continue":
		m.alwaysKeepKids = true
	}
}

func (m *model) requestCancelTurn() tea.Cmd {
	if !m.busy {
		return nil
	}
	n := 0
	if m.sess != nil && m.sess.Team != nil {
		n = m.sess.Team.RunningCount()
	}
	if n == 0 || m.alwaysStopKids {
		m.doCancelTurn()
		return nil
	}
	if m.alwaysKeepKids {
		return nil
	}
	m.cancel = &cancelTurnState{Running: n}
	m.cancelCursor = 0
	m.overlay = overlayCancelTurn
	return nil
}

func (m *model) doCancelTurn() {
	if m.turnCancel != nil {
		m.turnCancel()
		m.turnCancel = nil
	}
	m.status = "turn cancelled"
}
