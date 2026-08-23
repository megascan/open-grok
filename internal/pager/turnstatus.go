package pager

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const stopButton = "[stop]"

// turnStatusVisible is true while a turn is running or idle watchers remain.
// Hidden when idle with nothing still running — matches views/turn_status.rs.
func (m *model) turnStatusVisible() bool {
	if m.busy || m.turnActivity != "" {
		return true
	}
	if m.plan != nil {
		return true
	}
	if m.perm != nil && !m.permParked {
		return true
	}
	if m.question != nil && m.overlay == overlayQuestion {
		return true
	}
	return m.watcherCount() > 0
}

func (m *model) watcherCount() int {
	n := 0
	for _, j := range m.loops {
		if !j.stopped {
			n++
		}
	}
	if m.sess != nil && m.sess.Team != nil {
		for _, a := range m.sess.Team.Snapshot() {
			if a.Status == "running" {
				n++
			}
		}
	}
	for _, w := range m.workflows {
		if w.isActive() {
			n++
		}
	}
	return n
}

func (m *model) setTurnActivity(kind, tool string) {
	if m.turnActivity != kind || m.turnToolTitle != tool {
		m.phaseStarted = time.Now()
	}
	m.turnActivity = kind
	m.turnToolTitle = tool
	switch kind {
	case "thinking":
		m.status = "Thinking…"
	case "responding":
		m.status = "Responding…"
	case "tool":
		m.status = turnToolLabel(tool)
	case "compacting":
		m.status = "Compacting…"
	case "cancelling":
		m.status = "Cancelling…"
	default:
		m.status = "Running…"
	}
}

func turnToolLabel(tool string) string {
	t := strings.TrimSpace(tool)
	if t == "" {
		return "Running…"
	}
	switch strings.ToLower(t) {
	case "bash", "run_command", "shell":
		return "Run command"
	default:
		return t
	}
}

func (m *model) turnStatusLabel() string {
	if m.plan != nil {
		if m.plan.HasPlan && strings.TrimSpace(m.plan.Plan) != "" {
			return "Waiting on plan approval"
		}
		return "No plan written — approve or request changes"
	}
	if m.perm != nil && !m.permParked {
		return "Waiting…"
	}
	if m.question != nil && m.overlay == overlayQuestion {
		return "Waiting…"
	}
	if !m.busy {
		if n := m.watcherCount(); n > 0 {
			if n == 1 {
				return "1 still running"
			}
			return fmt.Sprintf("%d still running", n)
		}
		return ""
	}
	switch m.turnActivity {
	case "thinking":
		return "Thinking…"
	case "responding":
		return "Responding…"
	case "tool":
		return turnToolLabel(m.turnToolTitle)
	case "compacting":
		return "Compacting…"
	case "cancelling":
		return "Cancelling…"
	default:
		return "Running…"
	}
}

func formatTurnElapsed(d time.Duration) string {
	return formatDuration(d)
}

// renderTurnStatus paints the single-row activity line between scrollback
// and the prompt: spinner, label, phase timer, queued hint, [stop].
func (m *model) renderTurnStatus() string {
	if !m.turnStatusVisible() {
		m.stopBtnX, m.stopBtnW = -1, 0
		return ""
	}
	w := max(24, m.width)
	label := m.turnStatusLabel()
	left := ""
	if m.busy {
		left = m.spin.View() + " "
	} else {
		left = dimStyle.Render("○") + " "
	}
	styled := dimStyle.Render(label)
	if m.turnActivity == "tool" {
		styled = okStyle.Render(label)
	} else if m.turnActivity == "cancelling" {
		styled = errStyle.Render(label)
	}
	left += styled
	if !m.phaseStarted.IsZero() && m.busy {
		left += " " + dimStyle.Render(formatTurnElapsed(time.Since(m.phaseStarted)))
	}
	if n := len(m.queue); n > 0 && m.busy {
		left += " " + dimStyle.Render(fmt.Sprintf("· %d queued — Enter to send now", n))
	}
	right := ""
	if !m.turnStarted.IsZero() && m.busy {
		right = dimStyle.Render(formatTurnElapsed(time.Since(m.turnStarted)))
	}
	showStop := m.busy
	stop := stopButton
	if m.stopHover {
		stop = errStyle.Render(stopButton)
	} else if showStop {
		stop = errStyle.Render(stopButton)
	}
	if showStop {
		if right != "" {
			right += " "
		}
		right += stop
	}
	lw := lipglossWidth(left)
	rw := lipglossWidth(right)
	gap := w - lw - rw
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + right
	if showStop {
		m.stopBtnW = utf8.RuneCountInString(stopButton)
		m.stopBtnX = w - m.stopBtnW
	} else {
		m.stopBtnX, m.stopBtnW = -1, 0
	}
	return line
}

func lipglossWidth(s string) int {
	return len([]rune(stripANSI(s)))
}
