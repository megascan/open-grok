package pager

import (
	"fmt"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/hooks"
)

const (
	hookCheck  = "\u2713"
	hookFail   = "\u2717"
	hookBack   = "\u21a9"
	hookIndent = "    "
)

type hookRun struct {
	Name    string
	Phase   string
	Status  hooks.Status
	Elapsed time.Duration
	Output  string
	Error   string
	Detail  string
}

type toolHookData struct {
	Pre  []hookRun
	Post []hookRun
}

func hookDataFrom(runs []hooks.Run) toolHookData {
	var d toolHookData
	for _, r := range runs {
		item := hookRun{
			Name:    r.Name,
			Phase:   r.Phase,
			Status:  r.Status,
			Elapsed: r.Elapsed,
			Output:  r.Output,
			Error:   r.Error,
			Detail:  r.Detail,
		}
		switch r.Phase {
		case "post_tool_use":
			d.Post = append(d.Post, item)
		default:
			d.Pre = append(d.Pre, item)
		}
	}
	return d
}

func (d toolHookData) hasContent() bool {
	return hookCounts(d.Pre).total()+hookCounts(d.Post).total() > 0
}

type hookCount struct{ success, blocked, failed int }

func (c hookCount) total() int { return c.success + c.blocked + c.failed }

func (c hookCount) completed() int { return c.success + c.blocked }

func hookCounts(runs []hookRun) hookCount {
	var c hookCount
	for _, r := range runs {
		switch r.Status {
		case hooks.StatusSuccess:
			c.success++
		case hooks.StatusBlocked:
			c.blocked++
		case hooks.StatusFailed:
			c.failed++
		}
	}
	return c
}

func (d toolHookData) inlineSuffix() string {
	c := hookCounts(d.Pre)
	c.success += hookCounts(d.Post).success
	c.blocked += hookCounts(d.Post).blocked
	c.failed += hookCounts(d.Post).failed
	if c.total() == 0 {
		return ""
	}
	var inner string
	done := c.completed()
	if done > 0 {
		inner = okStyle.Render(fmt.Sprintf("%d", done))
	}
	if done > 0 && c.failed > 0 {
		inner += dimStyle.Render("/")
	}
	if c.failed > 0 {
		inner += errStyle.Render(fmt.Sprintf("%d", c.failed))
	}
	return dimStyle.Render("  [hooks: ") + inner + dimStyle.Render("]")
}

func (d toolHookData) renderDetail() string {
	if !d.hasContent() {
		return ""
	}
	var b strings.Builder
	writeHookSection(&b, "pre_tool_use", d.Pre)
	writeHookSection(&b, "post_tool_use", d.Post)
	return strings.TrimRight(b.String(), "\n")
}

func writeHookSection(b *strings.Builder, name string, runs []hookRun) {
	if hookCounts(runs).total() == 0 {
		return
	}
	writeAccent(b, dimStyle.Bold(true).Render(hookIndent+name))
	for _, r := range runs {
		if r.Status == hooks.StatusSkipped {
			writeAccent(b, dimStyle.Render(hookIndent+"  - "+r.Name+" skipped"))
			continue
		}
		mark := hookCheck
		markStyle := okStyle
		extra := ""
		switch r.Status {
		case hooks.StatusFailed:
			mark = hookFail
			markStyle = errStyle
			extra = r.Error
		case hooks.StatusBlocked:
			mark = hookBack
			markStyle = toolStyle
			extra = r.Detail
		}
		ms := r.Elapsed.Milliseconds()
		line := hookIndent + "  " + markStyle.Render(mark+" ") + dimStyle.Render(r.Name+fmt.Sprintf(" (%dms)", ms))
		writeAccent(b, line)
		if extra != "" {
			for i, row := range strings.Split(extra, "\n") {
				if i >= 3 {
					break
				}
				writeAccent(b, errStyle.Render(hookIndent+"      "+row))
			}
		}
		if r.Output != "" && r.Status == hooks.StatusSuccess {
			for i, row := range strings.Split(r.Output, "\n") {
				if i >= 3 {
					break
				}
				writeAccent(b, dimStyle.Render(hookIndent+"      "+row))
			}
		}
	}
}

func applyHookChrome(card string, data toolHookData, folded bool) string {
	if !data.hasContent() {
		return card
	}
	lines := strings.Split(card, "\n")
	if len(lines) > 0 {
		lines[0] += data.inlineSuffix()
	}
	out := strings.Join(lines, "\n")
	if folded {
		return out
	}
	detail := data.renderDetail()
	if detail == "" {
		return out
	}
	return out + "\n" + detail
}
