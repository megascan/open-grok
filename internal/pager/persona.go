package pager

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	toml "github.com/pelletier/go-toml/v2"
)

type personaField int

const (
	fieldName personaField = iota
	fieldDesc
	fieldModel
	fieldEffort
	fieldIsolation
	fieldInstr
	fieldInstrFile
)

var personaFields = []personaField{
	fieldName, fieldDesc, fieldModel, fieldEffort, fieldIsolation, fieldInstr, fieldInstrFile,
}

func (f personaField) label() string {
	switch f {
	case fieldName:
		return "Name"
	case fieldDesc:
		return "Description"
	case fieldModel:
		return "Model"
	case fieldEffort:
		return "Effort"
	case fieldIsolation:
		return "Isolation"
	case fieldInstr:
		return "Instructions"
	case fieldInstrFile:
		return "Instr. file"
	}
	return ""
}

func (f personaField) next() personaField {
	return personaFields[(int(f)+1)%len(personaFields)]
}

func (f personaField) prev() personaField {
	return personaFields[(int(f)+len(personaFields)-1)%len(personaFields)]
}

func (f personaField) inlineEditable() bool {
	switch f {
	case fieldName, fieldDesc, fieldModel, fieldEffort, fieldIsolation:
		return true
	}
	return false
}

type personaIO struct {
	Name        string `toml:"name"`
	IOType      string `toml:"io_type"`
	Required    bool   `toml:"required"`
	Description string `toml:"description"`
}

type personaDetail struct {
	Name, Description, Model, Effort, Isolation, Instructions, InstrFile string
	Inputs, Outputs                                                      []personaIO
	Path                                                                 string
	Editable                                                             bool
	Scope                                                                string
	Field                                                                personaField
	Editing                                                              bool
	EditBuf                                                              string
	InstrExpand                                                          bool
	Message                                                              string
}

func (d *personaDetail) value(f personaField) string {
	switch f {
	case fieldName:
		return d.Name
	case fieldDesc:
		return d.Description
	case fieldModel:
		return d.Model
	case fieldEffort:
		return d.Effort
	case fieldIsolation:
		return d.Isolation
	case fieldInstr:
		return d.Instructions
	case fieldInstrFile:
		return d.InstrFile
	}
	return ""
}

func (d *personaDetail) setValue(f personaField, v string) {
	switch f {
	case fieldName:
		d.Name = v
	case fieldDesc:
		d.Description = v
	case fieldModel:
		d.Model = v
	case fieldEffort:
		d.Effort = v
	case fieldIsolation:
		d.Isolation = v
	case fieldInstr:
		d.Instructions = v
	case fieldInstrFile:
		d.InstrFile = v
	}
}

func personaDetailFromName(name string) *personaDetail {
	return &personaDetail{Name: name, Scope: "bundled"}
}

func loadPersonaDetail(path string, editable bool, scope string) *personaDetail {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		Name             string      `toml:"name"`
		Description      string      `toml:"description"`
		Model            string      `toml:"model"`
		ReasoningEffort  string      `toml:"reasoning_effort"`
		DefaultIsolation string      `toml:"default_isolation"`
		Instructions     string      `toml:"instructions"`
		InstructionsFile string      `toml:"instructions_file"`
		Inputs           []personaIO `toml:"inputs"`
		Outputs          []personaIO `toml:"outputs"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	name := doc.Name
	if name == "" {
		name = strings.TrimSuffix(baseName(path), ".toml")
	}
	return &personaDetail{
		Name: name, Description: doc.Description, Model: doc.Model,
		Effort: doc.ReasoningEffort, Isolation: doc.DefaultIsolation,
		Instructions: doc.Instructions, InstrFile: doc.InstructionsFile,
		Inputs: doc.Inputs, Outputs: doc.Outputs,
		Path: path, Editable: editable, Scope: scope,
	}
}

func baseName(path string) string {
	i := strings.LastIndexAny(path, "/\\")
	if i < 0 {
		return path
	}
	return path[i+1:]
}

func (d *personaDetail) save() error {
	if d.Path == "" {
		return fmt.Errorf("No source file to save to")
	}
	data, err := os.ReadFile(d.Path)
	if err != nil {
		return fmt.Errorf("Failed to read file: %v", err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil || doc == nil {
		doc = map[string]any{}
	}
	set := func(k, v string) {
		if strings.TrimSpace(v) == "" {
			delete(doc, k)
		} else {
			doc[k] = v
		}
	}
	set("name", d.Name)
	set("description", d.Description)
	set("instructions", d.Instructions)
	set("instructions_file", d.InstrFile)
	set("model", d.Model)
	set("reasoning_effort", d.Effort)
	set("default_isolation", d.Isolation)
	out, err := toml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(d.Path, out, 0o600)
}

func (m *model) renderPersonaDetail(w int) string {
	d := m.agents.detail
	w = min(100, max(44, w))
	var b strings.Builder
	b.WriteString(titleStyle.Render("persona: "+d.Name) + "  " + dimStyle.Render(d.Scope) + "\n")
	if d.Message != "" {
		b.WriteString(errStyle.Render(d.Message) + "\n")
	}
	for _, f := range personaFields {
		val := d.value(f)
		if d.Editing && d.Field == f {
			val = d.EditBuf + "█"
		}
		if val == "" {
			val = "—"
		}
		if f == fieldInstr && !d.InstrExpand && len(val) > 80 {
			val = truncate(val, 80)
		}
		line := fmt.Sprintf("%-12s  %s", f.label(), val)
		if d.Field == f {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if len(d.Inputs) > 0 {
		b.WriteString("\n" + titleStyle.Render("Inputs") + "\n")
		for _, io := range d.Inputs {
			req := ""
			if io.Required {
				req = ", required"
			}
			b.WriteString(fmt.Sprintf("  • %s (%s%s)\n", io.Name, emptyDefault(io.IOType, "file"), req))
			if io.Description != "" {
				b.WriteString("    " + dimStyle.Render(io.Description) + "\n")
			}
		}
	}
	if len(d.Outputs) > 0 {
		b.WriteString("\n" + titleStyle.Render("Outputs") + "\n")
		for _, io := range d.Outputs {
			b.WriteString(fmt.Sprintf("  • %s (%s)\n", io.Name, emptyDefault(io.IOType, "file")))
		}
	}
	if d.Path != "" {
		b.WriteString("\n" + dimStyle.Render("Source: "+d.Path) + "\n")
	}
	footer := "j/k nav  ·  esc back"
	if d.Editing {
		footer = "enter save  ·  esc cancel"
	} else if d.Editable {
		footer = "j/k nav  ·  e/enter edit  ·  esc back"
	}
	b.WriteString("\n" + dimStyle.Render(footer))
	return modalBox.Width(w).Render(b.String())
}

func emptyDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func (m *model) handlePersonaDetailKey(msg tea.KeyMsg) tea.Cmd {
	d := m.agents.detail
	d.Message = ""
	key := msg.String()
	if d.Editing {
		switch key {
		case "esc":
			d.Editing = false
			d.EditBuf = ""
		case "enter":
			d.setValue(d.Field, d.EditBuf)
			d.Editing = false
			if err := d.save(); err != nil {
				d.Message = err.Error()
			}
			if m.agents != nil {
				m.agents.reload(m.opts.CWD)
			}
		case "backspace":
			d.EditBuf = trimLastRune(d.EditBuf)
		default:
			if msg.Type == tea.KeyRunes {
				d.EditBuf += string(msg.Runes)
			}
		}
		return nil
	}
	instrScroll := d.Field == fieldInstr && d.InstrExpand
	switch key {
	case "esc", "q":
		if instrScroll {
			d.InstrExpand = false
			return nil
		}
		m.agents.detail = nil
	case "down", "j":
		d.Field = d.Field.next()
	case "up", "k":
		d.Field = d.Field.prev()
	case "e", "enter":
		if d.Field == fieldInstr {
			d.InstrExpand = !d.InstrExpand
			return nil
		}
		if !d.Editable {
			d.Message = "Bundled personas are read-only"
			return nil
		}
		if !d.Field.inlineEditable() {
			d.Message = "This field cannot be edited inline"
			return nil
		}
		cur := d.value(d.Field)
		if strings.ContainsAny(cur, "\n\r") {
			d.Message = "Multiline values must be edited in the source file"
			return nil
		}
		d.Editing = true
		d.EditBuf = cur
	}
	return nil
}
