package pager

import (
	"fmt"
	"strings"

	"github.com/megascan/open-grok/internal/tools"
)

func (m *model) renderTodoPane(width int) string {
	if len(m.todos) == 0 {
		return ""
	}
	counts := todoCounts(m.todos)
	denom := counts.pending + counts.inProgress + counts.completed
	var b strings.Builder
	b.WriteString(goalTitle.Render("TODOS"))
	if denom > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  %d/%d", counts.completed, denom)))
	}
	b.WriteByte('\n')
	visible := 0
	for _, t := range m.todos {
		if visible >= 10 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  … %d more", len(m.todos)-visible)) + "\n")
			break
		}
		icon := tools.TodoIcon(t.Status)
		line := icon + " " + truncate(t.Content, max(8, width-4))
		switch t.Status {
		case tools.TodoInProgress:
			line = toolStyle.Render(line)
		case tools.TodoCompleted:
			line = dimStyle.Render(line)
		case tools.TodoCancelled:
			line = errStyle.Render(line)
		}
		b.WriteString(line + "\n")
		visible++
	}
	if counts.pending == 0 && counts.inProgress == 0 {
		switch {
		case counts.cancelled == 0:
			b.WriteString(dimStyle.Render("All done.") + "\n")
		case counts.completed == 0:
			b.WriteString(dimStyle.Render(fmt.Sprintf("%d cancelled.", counts.cancelled)) + "\n")
		default:
			b.WriteString(dimStyle.Render(fmt.Sprintf("%d done. %d cancelled.", counts.completed, counts.cancelled)) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type todoCount struct {
	pending, inProgress, completed, cancelled int
}

func todoCounts(items []tools.TodoItem) todoCount {
	var c todoCount
	for _, t := range items {
		switch t.Status {
		case tools.TodoInProgress:
			c.inProgress++
		case tools.TodoCompleted:
			c.completed++
		case tools.TodoCancelled:
			c.cancelled++
		default:
			c.pending++
		}
	}
	return c
}
