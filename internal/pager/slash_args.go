package pager

import (
	"strings"
	"unicode"

	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/types"
)

// slashDraft is the command/args split of a composer line that starts with `/`.
type slashDraft struct {
	Cmd    slashCmd
	Token  string
	Args   string
	InArgs bool
	OK     bool
}

func parseSlashDraft(text string) slashDraft {
	raw := strings.TrimLeft(text, " \t")
	if !strings.HasPrefix(raw, "/") {
		return slashDraft{}
	}
	body := raw[1:]
	i := strings.IndexFunc(body, unicode.IsSpace)
	if i < 0 {
		token := strings.TrimSpace(body)
		cmd, ok := ResolveSlash(token)
		return slashDraft{Cmd: cmd, Token: token, OK: ok}
	}
	token := body[:i]
	args := strings.TrimSpace(body[i:])
	cmd, ok := ResolveSlash(token)
	return slashDraft{Cmd: cmd, Token: token, Args: args, InArgs: true, OK: ok}
}

func (m *model) slashDropdownRows() []dropdownRow {
	d := parseSlashDraft(m.ta.Value())
	if d.InArgs && d.OK {
		if rows := m.slashArgRows(d); len(rows) > 0 {
			return rows
		}
		return nil
	}
	hits := FuzzySlash(m.filter, m.screenMd)
	rows := make([]dropdownRow, 0, len(hits))
	for _, c := range hits {
		rows = append(rows, dropdownRow{
			Label:  "/" + c.Name,
			Help:   c.Help,
			Insert: "/" + c.Name + " ",
			Run:    "/" + c.Name,
		})
	}
	return rows
}

func (m *model) slashArgRows(d slashDraft) []dropdownRow {
	q := strings.ToLower(strings.TrimSpace(d.Args))
	var rows []dropdownRow
	switch d.Cmd.Name {
	case "model":
		rows = m.modelArgRows(d.Args)
	case "effort":
		cur := ""
		if m.sess != nil {
			cur = m.sess.Effort
		}
		rows = effortArgRows("/effort", d.Args, cur)
	case "theme":
		rows = themeArgRows(m.theme)
	case "announcements":
		rows = []dropdownRow{
			{Label: "hide", Help: "Hide the announcement banner", Insert: "/announcements hide ", Run: "/announcements hide"},
			{Label: "show", Help: "Show the announcement banner", Insert: "/announcements show ", Run: "/announcements show"},
		}
	case "debug":
		rows = []dropdownRow{
			{Label: "scroll", Help: "Toggle the scroll-diagnostics HUD", Insert: "/debug scroll ", Run: "/debug scroll"},
			{Label: "fps", Help: "Toggle the FPS overlay", Insert: "/debug fps ", Run: "/debug fps"},
			{Label: "log", Help: "Toggle the scroll flight recorder (JSONL)", Insert: "/debug log ", Run: "/debug log"},
		}
	case "login":
		for _, p := range types.AllProviders {
			slug := p.CLIName()
			rows = append(rows, dropdownRow{
				Label:  loginPickerLabel(p),
				Help:   loginPickerDetail(p),
				Insert: "/login " + slug + " ",
				Run:    "/login " + slug,
			})
		}
	case "logout":
		rows = []dropdownRow{{
			Label: "codex", Help: "Disconnect the OpenAI Codex account",
			Insert: "/logout codex ", Run: "/logout codex",
		}}
	case "docs":
		rows = []dropdownRow{
			{Label: "how-to", Help: "Browse in-TUI How-to Guides", Insert: "/docs how-to ", Run: "/docs"},
			{Label: "web", Help: "Open docs.x.ai/build in the browser", Insert: "/docs web ", Run: "/docs web"},
		}
		for _, it := range docsItems() {
			rows = append(rows, dropdownRow{
				Label: it.Label, Help: "Open \"" + it.Label + "\"",
				Insert: "/docs " + it.Label + " ", Run: "/docs " + it.Label,
			})
		}
	default:
		return nil
	}
	if q == "" {
		return rows
	}
	var hits []dropdownRow
	for _, r := range rows {
		blob := strings.ToLower(r.Label + " " + r.Help + " " + r.Insert)
		if scoreName(q, strings.ToLower(r.Label)) > 0 || strings.Contains(blob, q) {
			hits = append(hits, r)
		}
	}
	return hits
}

func (m *model) modelArgRows(args string) []dropdownRow {
	if id, effortQ, ok := detectModelEffortPhase(args); ok {
		prefix := "/model " + id
		cur := ""
		if m.sess != nil {
			cur = m.sess.Effort
		}
		return effortArgRows(prefix, effortQ, cur)
	}
	q := strings.ToLower(strings.TrimSpace(args))
	var rows []dropdownRow
	for _, mod := range catalog.All() {
		if q != "" {
			blob := strings.ToLower(mod.ID + " " + mod.Model + " " + mod.Name)
			if scoreName(q, strings.ToLower(mod.ID)) == 0 && scoreName(q, strings.ToLower(mod.Name)) == 0 && !strings.Contains(blob, q) {
				continue
			}
		}
		rows = append(rows, dropdownRow{
			Label:  mod.Name,
			Help:   string(mod.Provider) + "  " + mod.ID,
			Insert: "/model " + mod.ID + " ",
			Run:    "/model " + mod.ID,
		})
	}
	return rows
}

func detectModelEffortPhase(args string) (modelID, effortQ string, ok bool) {
	lower := strings.ToLower(args)
	for _, mod := range catalog.All() {
		for _, key := range []string{mod.ID, mod.Model, mod.Name} {
			if key == "" {
				continue
			}
			prefix := strings.ToLower(key) + " "
			if strings.HasPrefix(lower, prefix) {
				return mod.ID, strings.TrimSpace(args[len(key):]), true
			}
		}
	}
	return "", "", false
}

func effortArgRows(cmdPrefix, query, current string) []dropdownRow {
	q := strings.ToLower(strings.TrimSpace(query))
	var rows []dropdownRow
	for _, lvl := range effortLevels {
		if q != "" && !strings.HasPrefix(lvl, q) && !strings.Contains(lvl, q) {
			continue
		}
		help := lvl
		if current != "" && strings.EqualFold(current, lvl) {
			help += " (active)"
		}
		rows = append(rows, dropdownRow{
			Label:  lvl,
			Help:   help,
			Insert: strings.TrimRight(cmdPrefix, " ") + " " + lvl + " ",
			Run:    strings.TrimRight(cmdPrefix, " ") + " " + lvl,
		})
	}
	return rows
}

func themeArgRows(current string) []dropdownRow {
	isAuto := strings.EqualFold(strings.TrimSpace(current), "auto") || strings.EqualFold(current, "system")
	resolved := resolveTheme(current).Name
	rows := []dropdownRow{{
		Label:  "auto",
		Help:   themeArgHelp("auto", isAuto),
		Insert: "/theme auto ",
		Run:    "/theme auto",
	}}
	for _, name := range themeCanonicalNames() {
		active := !isAuto && canonicalThemeName(resolved) == name
		rows = append(rows, dropdownRow{
			Label:  name,
			Help:   themeArgHelp(name, active),
			Insert: "/theme " + name + " ",
			Run:    "/theme " + name,
		})
	}
	return rows
}

func themeArgHelp(name string, active bool) string {
	help := name
	if name == "auto" {
		help = "auto (follow system)"
	}
	if active {
		help += " (active)"
	}
	return help
}
