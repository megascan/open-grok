package diagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Diagnostic IDs match the Rust FIX_REGISTRY handles (`terminal.<handle>`).
var (
	SSHWrapID          = DiagnosticID{Namespace: "terminal", Handle: "ssh-wrap"}
	TmuxClipboardID    = DiagnosticID{Namespace: "terminal", Handle: "tmux-clipboard"}
	DCSPassthroughID   = DiagnosticID{Namespace: "terminal", Handle: "dcs-passthrough"}
	TmuxExtendedKeysID = DiagnosticID{Namespace: "terminal", Handle: "tmux-extended-keys"}
	TmuxTruecolorID    = DiagnosticID{Namespace: "terminal", Handle: "tmux-truecolor"}
)

const (
	TmuxClipboardLine    = "set -g set-clipboard on"
	DCSPassthroughLine   = "set -wg allow-passthrough on"
	TmuxExtendedKeysLine = "set -g extended-keys on"
	TmuxTruecolorLine    = "set -as terminal-features \",*:RGB\""
	SSHWrapAliasPOSIX    = "alias ssh='entropy wrap ssh'"
	SSHWrapAliasFish     = "alias ssh 'entropy wrap ssh'"
	SSHWrapOneOff        = "entropy wrap ssh <host>"

	managedBegin = "# >>> entropy doctor >>>"
	managedEnd   = "# <<< entropy doctor <<<"
)

// Usage is the user-facing `/doctor` / CLI usage line (Rust-identical handles).
const Usage = "Usage: /doctor [fix [ssh-wrap|tmux-clipboard|dcs-passthrough|tmux-extended-keys]]"

// CLIUsage is the standalone command usage.
const CLIUsage = "Usage: entropy doctor [fix [ssh-wrap|tmux-clipboard|dcs-passthrough|tmux-extended-keys]]"

// DiagnosticID is `namespace.handle`, e.g. terminal.ssh-wrap.
type DiagnosticID struct {
	Namespace string
	Handle    string
}

func (id DiagnosticID) String() string {
	if id.Namespace == "" {
		return id.Handle
	}
	return id.Namespace + "." + id.Handle
}

// FixSpec is one automatic doctor fix.
type FixSpec struct {
	ID    DiagnosticID
	Label string
	Line  string
	Kind  string // ssh-wrap | tmux
}

var fixRegistry = []FixSpec{
	{ID: SSHWrapID, Label: "Set up local SSH wrapping", Line: SSHWrapAliasPOSIX, Kind: "ssh-wrap"},
	{ID: TmuxClipboardID, Label: "Enable tmux clipboard forwarding", Line: TmuxClipboardLine, Kind: "tmux"},
	{ID: DCSPassthroughID, Label: "Enable tmux DCS passthrough", Line: DCSPassthroughLine, Kind: "tmux"},
	{ID: TmuxExtendedKeysID, Label: "Enable tmux extended keys", Line: TmuxExtendedKeysLine, Kind: "tmux"},
	{ID: TmuxTruecolorID, Label: "Enable tmux truecolor passthrough", Line: TmuxTruecolorLine, Kind: "tmux"},
}

// ResolveFixID accepts `ssh-wrap` or `terminal.ssh-wrap`.
func ResolveFixID(value string) (DiagnosticID, error) {
	value = strings.TrimSpace(value)
	for _, spec := range fixRegistry {
		if value == spec.ID.Handle || value == spec.ID.String() {
			return spec.ID, nil
		}
	}
	return DiagnosticID{}, fmt.Errorf("unknown doctor fix %q", value)
}

// HumanFixCommand is the user-facing automatic-setup command.
func HumanFixCommand(id DiagnosticID) string {
	return "entropy doctor fix " + id.Handle
}

// AutomaticFixChoices is handle + label for `/doctor fix` listing.
func AutomaticFixChoices() []FixSpec {
	out := make([]FixSpec, len(fixRegistry))
	copy(out, fixRegistry)
	return out
}

func specFor(id DiagnosticID) (FixSpec, bool) {
	for _, spec := range fixRegistry {
		if spec.ID == id {
			return spec, true
		}
	}
	return FixSpec{}, false
}

// FormatFixList is the `/doctor fix` listing.
func FormatFixList() string {
	var b strings.Builder
	b.WriteString("Automatic fixes\n")
	for _, spec := range fixRegistry {
		b.WriteString(fmt.Sprintf("  %-20s %s\n", spec.ID.Handle, spec.Label))
	}
	b.WriteString("\nRun `entropy doctor fix <id>` (add --yes to apply).\n")
	return b.String()
}

// Plan describes a pending write.
type Plan struct {
	ID        DiagnosticID
	Path      string
	Line      string
	Preview   string
	WillWrite bool
	AlreadyOK bool
}

func (p Plan) SuccessMessage() string {
	if p.AlreadyOK {
		return fmt.Sprintf("Already healthy: `%s` is present in `%s`.", p.Line, p.Path)
	}
	return fmt.Sprintf("Added `%s` to `%s`.", p.Line, p.Path)
}

// FormatFixPreview is the question-card preview for `/doctor fix` (Rust format_fix_preview).
func FormatFixPreview(p Plan) string {
	var b strings.Builder
	b.WriteString("Doctor Fix\n\n")
	fmt.Fprintf(&b, "Fix: %s\n", p.ID.String())
	fmt.Fprintf(&b, "File: %s\n", p.Path)
	if p.WillWrite {
		fmt.Fprintf(&b, "\nText to add:\n%s\n", p.Line)
	} else {
		b.WriteString("\nText to add: None. The requested setting is already configured.\n")
	}
	return b.String()
}

// PlanFix builds the managed-block write without touching disk.
func PlanFix(id DiagnosticID) (Plan, error) {
	spec, ok := specFor(id)
	if !ok {
		return Plan{}, fmt.Errorf("unknown doctor fix %q", id.String())
	}
	path, line := targetFor(spec)
	existing, _ := os.ReadFile(path)
	body := string(existing)
	if alreadyHealthy(spec, body) {
		return Plan{ID: id, Path: path, Line: line, AlreadyOK: true, Preview: body, WillWrite: false}, nil
	}
	next := upsertManaged(body, spec.ID.String(), line)
	return Plan{
		ID:        id,
		Path:      path,
		Line:      line,
		Preview:   next,
		WillWrite: next != body,
	}, nil
}

func targetFor(spec FixSpec) (path, line string) {
	if spec.Kind == "ssh-wrap" {
		path = shellRCPath()
		if filepath.Base(os.Getenv("SHELL")) == "fish" {
			return path, SSHWrapAliasFish
		}
		return path, SSHWrapAliasPOSIX
	}
	return tmuxConfigPath(), spec.Line
}

func alreadyHealthy(spec FixSpec, body string) bool {
	switch spec.ID.Handle {
	case "ssh-wrap":
		return strings.Contains(body, "entropy wrap ssh") || strings.Contains(body, "open-grok wrap ssh")
	case "tmux-clipboard":
		return hasHealthyAssignment(body, "set-clipboard", []string{"on", "external"})
	case "dcs-passthrough":
		return hasHealthyAssignment(body, "allow-passthrough", []string{"on", "all"})
	case "tmux-extended-keys":
		return hasHealthyAssignment(body, "extended-keys", []string{"on"})
	case "tmux-truecolor":
		return strings.Contains(body, "terminal-features") && strings.Contains(body, "RGB")
	}
	return false
}

func hasHealthyAssignment(body, option string, values []string) bool {
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		if !strings.Contains(trim, option) {
			continue
		}
		for _, v := range values {
			if strings.HasSuffix(trim, " "+v) || strings.Contains(trim, option+" "+v) {
				return true
			}
		}
	}
	return false
}

func upsertManaged(body, itemID, line string) string {
	itemBegin := "# >>> " + itemID + " >>>"
	itemEnd := "# <<< " + itemID + " <<<"
	block := itemBegin + "\n" + line + "\n" + itemEnd
	if i := strings.Index(body, itemBegin); i >= 0 {
		if j := strings.Index(body[i:], itemEnd); j >= 0 {
			j = i + j + len(itemEnd)
			return strings.TrimRight(body[:i], "\n") + "\n" + block + body[j:]
		}
	}
	if i := strings.Index(body, managedBegin); i >= 0 {
		if j := strings.Index(body[i:], managedEnd); j >= 0 {
			j = i + j
			inner := body[i+len(managedBegin) : j]
			inner = strings.Trim(inner, "\n")
			if inner != "" {
				inner += "\n"
			}
			return body[:i] + managedBegin + "\n" + inner + block + "\n" + body[j:]
		}
	}
	suffix := managedBegin + "\n" + block + "\n" + managedEnd + "\n"
	if strings.TrimSpace(body) == "" {
		return suffix
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body + suffix
}

// ApplyFix writes the planned managed block. The caller supplies the user HOME
// via the process environment (tests set HOME to a temp dir).
func ApplyFix(id DiagnosticID) (Plan, error) {
	plan, err := PlanFix(id)
	if err != nil {
		return Plan{}, err
	}
	if plan.AlreadyOK || !plan.WillWrite {
		return plan, nil
	}
	if err := os.MkdirAll(filepath.Dir(plan.Path), 0o755); err != nil {
		return plan, err
	}
	if err := os.WriteFile(plan.Path, []byte(plan.Preview), 0o644); err != nil {
		return plan, err
	}
	return plan, nil
}

// FormatPlan is the CLI preview (no write).
func FormatPlan(p Plan) string {
	if p.AlreadyOK {
		return p.SuccessMessage() + "\n"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Will add `%s` to `%s`.\n", p.Line, p.Path))
	b.WriteString("Re-run with --yes to apply.\n")
	return b.String()
}
