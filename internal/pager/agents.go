package pager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/config"
	toml "github.com/pelletier/go-toml/v2"
)

type agentsTab int

const (
	agentsTabAgents agentsTab = iota
	agentsTabPersonas
)

func (t agentsTab) label() string {
	if t == agentsTabPersonas {
		return "Personas"
	}
	return "Agents"
}

type agentListEntry struct {
	Name, Desc, Scope, Path string
	Builtin                 bool
}

type personaListEntry struct {
	Name, Desc, Scope, Path string
	Editable                bool
}

type personaCreateField int

const (
	createName personaCreateField = iota
	createDesc
	createInstr
	createScope
)

type personaCreate struct {
	Name, Desc, Instr string
	Project           bool
	Field             personaCreateField
	Err               string
}

type agentsModal struct {
	tab      agentsTab
	agents   []agentListEntry
	personas []personaListEntry
	selA     int
	selP     int
	query    string
	filterOn bool
	create   *personaCreate
	detail   *personaDetail
	message  string
	confirm  string // delete path
}

var builtinAgents = []agentListEntry{
	{Name: "grok-build", Desc: "Entropy agent for software engineering tasks.", Scope: "builtin", Builtin: true},
	{Name: "grok-build-concise", Desc: "Entropy agent with concise output format.", Scope: "builtin", Builtin: true},
	{Name: "grok-build-plan", Desc: "Entropy agent with plan mode support.", Scope: "builtin", Builtin: true},
	{Name: "grok-build-plan-no-subagents", Desc: "Entropy agent with plan mode (no subagents).", Scope: "builtin", Builtin: true},
	{Name: "grok-build-ask-user", Desc: "Entropy agent with ask-user-question tool.", Scope: "builtin", Builtin: true},
	{Name: "codex", Desc: "Codex toolset and prompt", Scope: "builtin", Builtin: true},
	{Name: "opencode", Desc: "OpenCode toolset — opencode-style tools and parameter conventions", Scope: "builtin", Builtin: true},
	{Name: "general-purpose", Desc: "General-purpose subagent", Scope: "builtin", Builtin: true},
	{Name: "explore", Desc: "Explore codebase quickly", Scope: "builtin", Builtin: true},
	{Name: "plan", Desc: "Planning subagent", Scope: "builtin", Builtin: true},
	{Name: "browser-use", Desc: "Browser-use subagent", Scope: "builtin", Builtin: true},
	{Name: "grok-build-orchestrator", Desc: "Orchestrator for parallel Entropy subagents", Scope: "builtin", Builtin: true},
}

func scanConfigDir(paths []string, exts ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range paths {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			ok := false
			for _, ext := range exts {
				if strings.HasSuffix(strings.ToLower(name), ext) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
			p := filepath.Join(dir, name)
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func firstTOMLString(path string, keys ...string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return strings.TrimSpace(string(data))
	}
	for _, k := range keys {
		if v, ok := doc[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func loadAgentEntries(cwd string) []agentListEntry {
	out := append([]agentListEntry{}, builtinAgents...)
	user := filepath.Join(config.Home(), "agents")
	paths := append([]string{user}, config.ProjectSubdir(cwd, "agents")...)
	for _, p := range scanConfigDir(paths, ".md", ".toml") {
		name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		scope := "user"
		if strings.Contains(p, string(filepath.Separator)+".entropy"+string(filepath.Separator)) ||
			strings.Contains(p, string(filepath.Separator)+".opengrok"+string(filepath.Separator)) {
			if !strings.HasPrefix(p, config.Home()) {
				scope = "project"
			}
		}
		desc := firstTOMLString(p, "description")
		if desc == "" {
			data, _ := os.ReadFile(p)
			desc = truncate(strings.TrimSpace(string(data)), 80)
		}
		out = append(out, agentListEntry{Name: name, Desc: desc, Scope: scope, Path: p})
	}
	return out
}

func loadPersonaEntries(cwd string) []personaListEntry {
	var out []personaListEntry
	user := filepath.Join(config.Home(), "personas")
	paths := append([]string{user}, config.ProjectSubdir(cwd, "personas")...)
	for _, p := range scanConfigDir(paths, ".toml") {
		name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		scope := "user"
		if !strings.HasPrefix(p, config.Home()) {
			scope = "project"
		}
		desc := firstTOMLString(p, "description", "name")
		if nm := firstTOMLString(p, "name"); nm != "" {
			name = nm
		}
		out = append(out, personaListEntry{Name: name, Desc: desc, Scope: scope, Path: p, Editable: true})
	}
	return out
}

func (s *agentsModal) reload(cwd string) {
	s.agents = loadAgentEntries(cwd)
	s.personas = loadPersonaEntries(cwd)
	if s.selA >= len(s.agents) {
		s.selA = 0
	}
	if s.selP >= len(s.personas) {
		s.selP = 0
	}
}

func (s *agentsModal) filteredAgents() []agentListEntry {
	if s.query == "" {
		return s.agents
	}
	q := strings.ToLower(s.query)
	var out []agentListEntry
	for _, e := range s.agents {
		if strings.Contains(strings.ToLower(e.Name+" "+e.Desc+" "+e.Scope), q) {
			out = append(out, e)
		}
	}
	return out
}

func (s *agentsModal) filteredPersonas() []personaListEntry {
	if s.query == "" {
		return s.personas
	}
	q := strings.ToLower(s.query)
	var out []personaListEntry
	for _, e := range s.personas {
		if strings.Contains(strings.ToLower(e.Name+" "+e.Desc+" "+e.Scope), q) {
			out = append(out, e)
		}
	}
	return out
}

func (m *model) openAgents(tab agentsTab) {
	s := &agentsModal{tab: tab}
	s.reload(m.opts.CWD)
	m.agents = s
	m.overlay = overlayAgents
}

func (m *model) renderAgents(w int) string {
	s := m.agents
	if s == nil {
		return ""
	}
	if s.detail != nil {
		return m.renderPersonaDetail(w)
	}
	w = min(100, max(44, w))
	var b strings.Builder
	b.WriteString(titleStyle.Render("Agents") + "\n")
	for _, t := range []agentsTab{agentsTabAgents, agentsTabPersonas} {
		lab := " " + t.label() + " "
		if t == s.tab {
			b.WriteString(selStyle.Render(lab))
		} else {
			b.WriteString(dimStyle.Render(lab))
		}
		b.WriteByte(' ')
	}
	b.WriteByte('\n')
	if s.message != "" {
		b.WriteString(errStyle.Render(s.message) + "\n")
	}
	if s.create != nil {
		b.WriteString(m.renderPersonaCreate(s.create, w))
		return modalBox.Width(w).Render(b.String())
	}
	if s.filterOn || s.query != "" {
		q := s.query
		if s.filterOn {
			q += "█"
		}
		b.WriteString(dimStyle.Render("filter: ") + q + "\n")
	}
	b.WriteByte('\n')
	if s.tab == agentsTabAgents {
		items := s.filteredAgents()
		if s.selA >= len(items) {
			s.selA = 0
		}
		if len(items) == 0 {
			b.WriteString(dimStyle.Render("(no agents)") + "\n")
		}
		start, limit := 0, 14
		if s.selA >= limit {
			start = s.selA - limit + 1
		}
		for i, e := range items {
			if i < start || i >= start+limit {
				continue
			}
			line := e.Name + "  " + dimStyle.Render(e.Scope)
			if e.Desc != "" {
				line += "\n    " + dimStyle.Render(truncate(e.Desc, w-8))
			}
			if i == s.selA {
				line = selStyle.Render(e.Name+"  "+e.Scope) + "\n    " + dimStyle.Render(truncate(e.Desc, w-8))
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + dimStyle.Render("tab switch  ·  enter/o view  ·  / search  ·  esc"))
	} else {
		items := s.filteredPersonas()
		if s.selP >= len(items) {
			s.selP = 0
		}
		if len(items) == 0 {
			b.WriteString(dimStyle.Render("(no personas — n to create)") + "\n")
		}
		start, limit := 0, 14
		if s.selP >= limit {
			start = s.selP - limit + 1
		}
		for i, e := range items {
			if i < start || i >= start+limit {
				continue
			}
			line := e.Name + "  " + dimStyle.Render(e.Scope)
			if i == s.selP {
				if s.confirm == e.Path {
					line = errStyle.Render(e.Name + "  d again to delete")
				} else {
					line = selStyle.Render(e.Name + "  " + e.Scope)
				}
			}
			if e.Desc != "" && i != s.selP {
				line += "  " + dimStyle.Render(truncate(e.Desc, 40))
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + dimStyle.Render("tab switch  ·  enter/o detail  ·  n new  ·  d delete  ·  / search  ·  esc"))
	}
	return modalBox.Width(w).Render(b.String())
}

func (m *model) renderPersonaCreate(c *personaCreate, w int) string {
	field := func(id personaCreateField, label, val string) string {
		mark := "  "
		if c.Field == id {
			mark = "▸ "
			val += "█"
		}
		return mark + label + "  " + val + "\n"
	}
	scope := "user  (~/.entropy/personas)"
	if c.Project {
		scope = "project  (.entropy/personas)"
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("New persona") + "\n")
	if c.Err != "" {
		b.WriteString(errStyle.Render(c.Err) + "\n")
	}
	b.WriteString(field(createName, "Name", c.Name))
	b.WriteString(field(createDesc, "Description", c.Desc))
	b.WriteString(field(createInstr, "Instructions", c.Instr))
	b.WriteString(field(createScope, "Scope", scope))
	b.WriteString("\n" + dimStyle.Render("tab fields  ·  space/←/→ scope  ·  enter create  ·  esc cancel"))
	_ = w
	return b.String()
}

func (m *model) handleAgentsKey(msg tea.KeyMsg) tea.Cmd {
	s := m.agents
	if s == nil {
		m.overlay = overlayNone
		return nil
	}
	if s.detail != nil {
		return m.handlePersonaDetailKey(msg)
	}
	key := msg.String()
	if s.create != nil {
		return m.handlePersonaCreateKey(msg)
	}
	if s.filterOn {
		switch key {
		case "esc":
			s.filterOn = false
		case "backspace":
			s.query = trimLastRune(s.query)
		default:
			if msg.Type == tea.KeyRunes {
				s.query += string(msg.Runes)
			}
		}
		return nil
	}
	switch key {
	case "esc", "q":
		m.overlay = overlayNone
		m.agents = nil
	case "tab", "right", "l":
		if s.tab == agentsTabAgents {
			s.tab = agentsTabPersonas
		} else {
			s.tab = agentsTabAgents
		}
	case "shift+tab", "left", "h":
		if s.tab == agentsTabAgents {
			s.tab = agentsTabPersonas
		} else {
			s.tab = agentsTabAgents
		}
	case "down", "j":
		if s.tab == agentsTabAgents {
			if s.selA+1 < len(s.filteredAgents()) {
				s.selA++
			}
		} else if s.selP+1 < len(s.filteredPersonas()) {
			s.selP++
		}
	case "up", "k":
		if s.tab == agentsTabAgents {
			if s.selA > 0 {
				s.selA--
			}
		} else if s.selP > 0 {
			s.selP--
		}
	case "/", "i":
		s.filterOn = true
	case "n":
		if s.tab == agentsTabPersonas {
			s.create = &personaCreate{}
		}
	case "d":
		if s.tab == agentsTabPersonas {
			items := s.filteredPersonas()
			if s.selP < len(items) && items[s.selP].Editable {
				p := items[s.selP]
				if s.confirm == p.Path {
					_ = os.Remove(p.Path)
					s.confirm = ""
					s.reload(m.opts.CWD)
				} else {
					s.confirm = p.Path
				}
			} else if s.selP < len(items) && !items[s.selP].Editable {
				s.message = "Cannot delete bundled personas"
			}
		}
	case "enter", "o":
		if s.tab == agentsTabAgents {
			items := s.filteredAgents()
			if s.selA < len(items) {
				e := items[s.selA]
				title := e.Name
				body := e.Desc
				if e.Path != "" {
					if data, err := os.ReadFile(e.Path); err == nil {
						body = string(data)
					}
					return m.openLineViewerContent(e.Path, title, body, false)
				}
				return m.openLineViewerContent("", title, "# "+e.Name+"\n\n"+e.Desc+"\n\nScope: builtin\n", false)
			}
		} else {
			items := s.filteredPersonas()
			if s.selP < len(items) {
				e := items[s.selP]
				s.detail = loadPersonaDetail(e.Path, e.Editable, e.Scope)
				if s.detail == nil {
					s.detail = personaDetailFromName(e.Name)
				}
			}
		}
	}
	if key != "d" {
		s.confirm = ""
	}
	return nil
}

func (m *model) handlePersonaCreateKey(msg tea.KeyMsg) tea.Cmd {
	s := m.agents
	c := s.create
	key := msg.String()
	switch key {
	case "esc":
		s.create = nil
	case "tab", "down":
		c.Field = (c.Field + 1) % 4
	case "shift+tab", "up":
		c.Field = (c.Field + 3) % 4
	case "left", "right", " ":
		if c.Field == createScope {
			c.Project = !c.Project
		}
	case "enter":
		path, err := createPersonaTemplate(c.Name, c.Desc, c.Instr, c.Project, m.opts.CWD)
		if err != nil {
			c.Err = err.Error()
			return nil
		}
		s.create = nil
		s.reload(m.opts.CWD)
		s.tab = agentsTabPersonas
		for i, p := range s.personas {
			if p.Path == path {
				s.selP = i
				break
			}
		}
	case "backspace":
		switch c.Field {
		case createName:
			c.Name = trimLastRune(c.Name)
		case createDesc:
			c.Desc = trimLastRune(c.Desc)
		case createInstr:
			c.Instr = trimLastRune(c.Instr)
		}
	default:
		if msg.Type != tea.KeyRunes {
			return nil
		}
		ch := string(msg.Runes)
		switch c.Field {
		case createName:
			c.Name += ch
		case createDesc:
			c.Desc += ch
		case createInstr:
			c.Instr += ch
		}
	}
	return nil
}

func sanitizeConfigName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	var b strings.Builder
	alnum := false
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			alnum = true
		case r == '-' || r == '_' || unicode.IsSpace(r):
			b.WriteByte('-')
		}
	}
	out := strings.Trim(strings.ReplaceAll(b.String(), "--", "-"), "-")
	if !alnum {
		return "", fmt.Errorf("Name must contain at least one alphanumeric character")
	}
	return out, nil
}

func createPersonaTemplate(name, desc, instr string, project bool, cwd string) (string, error) {
	sanitized, err := sanitizeConfigName(name)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(config.Home(), "personas")
	if project {
		dir = filepath.Join(cwd, config.ProjectDirName(), "personas")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("Failed to create personas directory: %v", err)
	}
	path := filepath.Join(dir, sanitized+".toml")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("Persona '%s' already exists", sanitized)
	}
	doc := map[string]any{}
	if strings.TrimSpace(desc) != "" {
		doc["description"] = strings.TrimSpace(desc)
	}
	if strings.TrimSpace(instr) != "" {
		doc["instructions"] = strings.TrimSpace(instr)
	}
	data, err := toml.Marshal(doc)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("Failed to write persona file: %v", err)
	}
	return path, nil
}
