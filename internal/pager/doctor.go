package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/diagnostics"
)

func (m *model) renderDoctor(w int) string {
	if len(m.items) > 0 {
		return m.renderListModal(m.overlayTitle(), w)
	}
	body := m.doctorReport
	if strings.TrimSpace(body) == "" {
		body = "(empty)"
	}
	lines := strings.Split(body, "\n")
	limit := 22
	start := m.cursor
	if start > len(lines) {
		start = 0
		m.cursor = 0
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	writeAccent(&b, titleStyle.Render("Doctor"))
	writeAccent(&b, "")
	writeAccentLines(&b, strings.Join(lines[start:end], "\n"))
	writeAccent(&b, "")
	writeAccent(&b, dimStyle.Render("j/k scroll  ·  /doctor fix  ·  esc close"))
	return modalBox.Width(w).Render(b.String())
}

func (m *model) openDoctor() {
	m.openDoctorReport()
}

func (m *model) openDoctorReport() {
	m.overlay = overlayDoctor
	m.cursor = 0
	m.filter = ""
	m.items = nil
	m.doctorReport = diagnostics.Format(diagnostics.Collect())
}

func (m *model) openDoctorFixes() {
	m.overlay = overlayDoctor
	m.cursor = 0
	m.filter = ""
	m.doctorReport = ""
	m.items = nil
	for _, spec := range diagnostics.AutomaticFixChoices() {
		m.items = append(m.items, listItem{
			Key:    spec.ID.Handle,
			Label:  spec.ID.Handle,
			Detail: spec.Label,
		})
	}
}

func (m *model) handleDoctor(args string) tea.Cmd {
	args = strings.TrimSpace(args)
	if args == "" {
		m.openDoctorReport()
		return nil
	}
	fields := strings.Fields(args)
	switch {
	case len(fields) == 1 && fields[0] == "fix":
		m.openDoctorFixes()
		return nil
	case len(fields) == 2 && fields[0] == "fix":
		id, err := diagnostics.ResolveFixID(fields[1])
		if err != nil {
			m.appendSys(err.Error() + "\n" + diagnostics.Usage)
			return nil
		}
		m.openConfirm("doctor-fix:" + id.Handle)
		return nil
	default:
		m.appendSys(diagnostics.Usage)
		return nil
	}
}

func (m *model) applyDoctorFix(handle string) {
	id, err := diagnostics.ResolveFixID(handle)
	if err != nil {
		m.appendSys(err.Error() + "\n" + diagnostics.Usage)
		return
	}
	plan, err := diagnostics.ApplyFix(id)
	if err != nil {
		m.appendSys(err.Error())
		return
	}
	m.appendSys(plan.SuccessMessage())
}

func doctorFixPreview(kind string) string {
	handle := strings.TrimPrefix(kind, "doctor-fix:")
	id, err := diagnostics.ResolveFixID(handle)
	if err != nil {
		return err.Error()
	}
	plan, err := diagnostics.PlanFix(id)
	if err != nil {
		return err.Error()
	}
	return diagnostics.FormatFixPreview(plan)
}

func doctorConfirmBody(kind string) string {
	return doctorFixPreview(kind)
}
