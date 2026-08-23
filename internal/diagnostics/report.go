// Package diagnostics ports the Rust pager /doctor report and automatic fixes.
package diagnostics

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/version"
	"golang.org/x/term"
)

const (
	DispositionIssue          = "issue"
	DispositionRecommendation = "recommendation"
)

// Finding is one doctor issue or recommendation.
type Finding struct {
	ID           string `json:"id"`
	Message      string `json:"message"`
	Disposition  string `json:"disposition"`
	Fix          string `json:"fix,omitempty"`
	ConfigPath   string `json:"config_path,omitempty"`
	AutomaticCmd string `json:"automatic_command,omitempty"`
	Note         string `json:"note,omitempty"`
}

// Report is the formatted /doctor snapshot.
type Report struct {
	Facts    Facts     `json:"facts"`
	Findings []Finding `json:"findings"`
}

// Facts are the Environment / Clipboard / Voice rows.
type Facts struct {
	Terminal           string `json:"terminal"`
	Xtversion          string `json:"xtversion,omitempty"`
	Multiplexer        string `json:"multiplexer"`
	Byobu              string `json:"byobu,omitempty"`
	SSH                bool   `json:"ssh"`
	Color              string `json:"color,omitempty"`
	Themes             string `json:"themes,omitempty"`
	Keyboard           string `json:"keyboard,omitempty"`
	Newline            string `json:"newline,omitempty"`
	NativeClip         string `json:"native_clipboard"`
	TmuxClip           bool   `json:"tmux_clipboard"`
	OSC52              string `json:"osc52"`
	WrapSink           bool   `json:"wrap"`
	DataControl        string `json:"data_control,omitempty"`
	ClipStatus         string `json:"clipboard_status"`
	Microphone         string `json:"microphone"`
	Home               string `json:"home"`
	Version            string `json:"version"`
	TmuxClipboardValue string `json:"tmux_set_clipboard,omitempty"`
	AllowPass          string `json:"tmux_allow_passthrough,omitempty"`
	ExtendedKeys       string `json:"tmux_extended_keys,omitempty"`
	Truecolor          string `json:"tmux_truecolor,omitempty"`
}

func inSSH() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CLIENT") != ""
}

func multiplexerName() string {
	if os.Getenv("TMUX") != "" {
		return "tmux"
	}
	if os.Getenv("STY") != "" {
		return "screen"
	}
	return "none"
}

func colorLevel() string {
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	if strings.Contains(ct, "truecolor") || strings.Contains(ct, "24bit") {
		return "truecolor"
	}
	term := strings.ToLower(os.Getenv("TERM"))
	if strings.Contains(term, "256color") || strings.Contains(term, "256") {
		return "256color"
	}
	if term != "" && term != "dumb" {
		return "ansi"
	}
	return ""
}

func nativeClipboard() string {
	switch runtime.GOOS {
	case "darwin":
		if lookPath("pbcopy") {
			if inSSH() {
				return "remote (pbcopy)"
			}
			return "local (pbcopy)"
		}
	case "windows":
		return "local (clip)"
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" && lookPath("wl-copy") {
			if inSSH() {
				return "remote (wl-copy)"
			}
			return "local (wl-copy)"
		}
		if lookPath("xclip") {
			if inSSH() {
				return "remote (xclip)"
			}
			return "local (xclip)"
		}
		if lookPath("xsel") {
			if inSSH() {
				return "remote (xsel)"
			}
			return "local (xsel)"
		}
	}
	if inSSH() {
		return "unavailable"
	}
	return "unavailable"
}

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func tmuxOption(args ...string) string {
	if os.Getenv("TMUX") == "" {
		return ""
	}
	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func osc52Label() string {
	if config.FirstEnv("ENTROPY_CLIPBOARD_NO_OSC52", "GROK_CLIPBOARD_NO_OSC52") != "" {
		return "off"
	}
	return "on"
}

func wrapSinkOn() bool {
	// The wrap CLI is always available on this binary; treat it as on when
	// the process is itself a wrap child (GROK/ENTROPY wrap markers) or when
	// the wrap command exists next to this binary.
	if os.Getenv("ENTROPY_WRAP") != "" || os.Getenv("GROK_WRAP") != "" {
		return true
	}
	return lookPath("entropy") || lookPath("open-grok")
}

func dataControl() string {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return ""
	}
	if lookPath("wl-copy") || os.Getenv("XDG_RUNTIME_DIR") != "" {
		if lookPath("wl-copy") {
			return "on"
		}
	}
	return "off"
}

// Collect gathers live doctor facts and findings for this process.
func Collect() Report {
	facts := Facts{
		Terminal:    strings.TrimSpace(os.Getenv("TERM")),
		Multiplexer: multiplexerName(),
		SSH:         inSSH(),
		Color:       colorLevel(),
		NativeClip:  nativeClipboard(),
		TmuxClip:    os.Getenv("TMUX") != "",
		OSC52:       osc52Label(),
		WrapSink:    wrapSinkOn(),
		DataControl: dataControl(),
		ClipStatus:  "unverified",
		Microphone:  "none detected",
		Home:        config.Home(),
		Version:     version.String(),
	}
	if facts.Terminal == "" {
		facts.Terminal = "unknown"
	}
	if os.Getenv("BYOBU") != "" || os.Getenv("BYOBU_BACKEND") != "" || os.Getenv("BYOBU_CONFIG_DIR") != "" {
		facts.Byobu = "yes"
	}
	if facts.Color == "truecolor" {
		facts.Themes = "all"
	}
	if os.Getenv("TMUX") != "" {
		facts.TmuxClipboardValue = tmuxOption("show-options", "-gqv", "set-clipboard")
		facts.AllowPass = tmuxOption("show-options", "-gqv", "allow-passthrough")
		if facts.AllowPass == "" {
			facts.AllowPass = tmuxOption("show-options", "-gv", "allow-passthrough")
		}
		facts.ExtendedKeys = tmuxOption("show-options", "-gqv", "extended-keys")
		facts.Truecolor = tmuxOption("show-options", "-gqv", "terminal-features")
		facts.TmuxClip = facts.TmuxClipboardValue == "on" || facts.TmuxClipboardValue == "external"
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		facts.ClipStatus = "unverified"
	}

	rep := Report{Facts: facts}
	rep.Findings = findings(facts)
	return rep
}

func findings(f Facts) []Finding {
	var out []Finding
	tmuxConf := tmuxConfigPath()
	if f.SSH {
		out = append(out, Finding{
			ID:          "terminal.ssh",
			Message:     "this session is already remote over SSH",
			Disposition: DispositionRecommendation,
			Note:        "wrap SSH from the local machine so OSC 52 and theme survive the hop",
			Fix:         "entropy wrap ssh <host>",
		})
	} else {
		spec, _ := specFor(SSHWrapID)
		rc := shellRCPath()
		body, _ := os.ReadFile(rc)
		if !alreadyHealthy(spec, string(body)) {
			out = append(out, Finding{
				ID:           SSHWrapID.String(),
				Message:      "SSH from this machine does not wrap Entropy, so OSC 52 clipboard and theme may drop on the remote side",
				Disposition:  DispositionRecommendation,
				AutomaticCmd: HumanFixCommand(SSHWrapID),
				Fix:          "alias ssh='entropy wrap ssh'",
				ConfigPath:   rc,
			})
		}
	}
	if os.Getenv("TMUX") != "" {
		if f.TmuxClipboardValue != "on" && f.TmuxClipboardValue != "external" {
			out = append(out, Finding{
				ID:           TmuxClipboardID.String(),
				Message:      "`set-clipboard` is off in tmux, so OSC 52 copies may not reach the outer terminal",
				Disposition:  DispositionIssue,
				AutomaticCmd: HumanFixCommand(TmuxClipboardID),
				Fix:          TmuxClipboardLine,
				ConfigPath:   tmuxConf,
			})
		}
		if f.AllowPass != "on" && f.AllowPass != "all" {
			out = append(out, Finding{
				ID:           DCSPassthroughID.String(),
				Message:      "`allow-passthrough` is off in tmux, which can block clipboard copies in nested sessions",
				Disposition:  DispositionIssue,
				AutomaticCmd: HumanFixCommand(DCSPassthroughID),
				Fix:          DCSPassthroughLine,
				ConfigPath:   tmuxConf,
			})
		}
		if f.ExtendedKeys != "on" {
			out = append(out, Finding{
				ID:           TmuxExtendedKeysID.String(),
				Message:      "`extended-keys` is off in tmux, so some shortcuts may not work",
				Disposition:  DispositionIssue,
				AutomaticCmd: HumanFixCommand(TmuxExtendedKeysID),
				Fix:          TmuxExtendedKeysLine,
				ConfigPath:   tmuxConf,
			})
		}
	}
	if config.NeverGrokHome(f.Home) {
		out = append(out, Finding{
			ID:          "home.grok",
			Message:     "home resolved to ~/.grok — this is forbidden",
			Disposition: DispositionIssue,
		})
	}
	return out
}

func tmuxConfigPath() string {
	if dir := os.Getenv("BYOBU_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".tmux.conf")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if os.Getenv("BYOBU") != "" || os.Getenv("BYOBU_BACKEND") != "" {
			return filepath.Join(home, ".byobu", ".tmux.conf")
		}
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		if st, err := os.Stat(filepath.Join(xdg, "tmux", "tmux.conf")); err == nil && !st.IsDir() {
			return filepath.Join(xdg, "tmux", "tmux.conf")
		}
		return filepath.Join(home, ".tmux.conf")
	}
	return "~/.tmux.conf"
}

func shellRCPath() string {
	shell := filepath.Base(os.Getenv("SHELL"))
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish")
	default:
		return filepath.Join(home, ".bashrc")
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// Format matches the Rust TUI `/doctor` report layout.
func Format(r Report) string {
	f := r.Facts
	var b strings.Builder
	b.WriteString("Environment\n")
	b.WriteString(fmt.Sprintf("  terminal     %s\n", f.Terminal))
	if f.Xtversion != "" {
		b.WriteString(fmt.Sprintf("  xtversion    %s\n", f.Xtversion))
	}
	b.WriteString(fmt.Sprintf("  multiplexer  %s\n", f.Multiplexer))
	if f.Byobu != "" {
		b.WriteString(fmt.Sprintf("  byobu        %s\n", f.Byobu))
	}
	b.WriteString(fmt.Sprintf("  ssh          %s\n", yesNo(f.SSH)))
	if f.Color != "" {
		b.WriteString(fmt.Sprintf("  color        %s\n", f.Color))
	}
	if f.Themes != "" {
		b.WriteString(fmt.Sprintf("  themes       %s\n", f.Themes))
	}
	if f.Keyboard != "" {
		b.WriteString(fmt.Sprintf("  keyboard     %s\n", f.Keyboard))
	}
	if f.Newline != "" {
		b.WriteString(fmt.Sprintf("  newline      %s\n", f.Newline))
	}
	b.WriteString(fmt.Sprintf("  home         %s\n", f.Home))
	b.WriteString(fmt.Sprintf("  version      %s\n", f.Version))

	b.WriteString("\nClipboard\n")
	b.WriteString(fmt.Sprintf("  native       %s\n", f.NativeClip))
	b.WriteString(fmt.Sprintf("  tmux         %s\n", onOff(f.TmuxClip)))
	b.WriteString(fmt.Sprintf("  osc 52       %s\n", f.OSC52))
	b.WriteString(fmt.Sprintf("  wrap         %s\n", onOff(f.WrapSink)))
	if f.DataControl != "" {
		b.WriteString(fmt.Sprintf("  data-control %s\n", f.DataControl))
	}
	b.WriteString(fmt.Sprintf("  status       %s\n", f.ClipStatus))

	b.WriteString("\nVoice\n")
	b.WriteString(fmt.Sprintf("  microphone   %s\n", f.Microphone))

	formatFindings(r, &b)
	return b.String()
}

func formatFindings(r Report, b *strings.Builder) {
	var issues, recs []Finding
	for _, f := range r.Findings {
		switch f.Disposition {
		case DispositionIssue:
			issues = append(issues, f)
		case DispositionRecommendation:
			recs = append(recs, f)
		}
	}
	if len(issues) == 0 {
		b.WriteString("\nNo issues found.\n")
	} else {
		b.WriteString(fmt.Sprintf("\nIssues (%d)\n", len(issues)))
		for _, f := range issues {
			formatFinding(b, f)
		}
	}
	if len(recs) > 0 {
		b.WriteString("\nRecommendations\n")
		for _, f := range recs {
			formatFinding(b, f)
		}
	}
}

func formatFinding(b *strings.Builder, f Finding) {
	marker := "i"
	if f.Disposition == DispositionIssue {
		marker = "!"
	}
	b.WriteString(fmt.Sprintf("\n  %s %s  %s\n", marker, f.ID, f.Message))
	if f.AutomaticCmd != "" {
		b.WriteString(fmt.Sprintf("      Automatic setup: `%s`\n", f.AutomaticCmd))
	}
	switch {
	case f.ConfigPath != "" && f.Fix != "":
		b.WriteString(fmt.Sprintf("      Add `%s` to %s\n", f.Fix, f.ConfigPath))
	case f.AutomaticCmd != "" && f.Fix != "":
		b.WriteString(fmt.Sprintf("      One-off: `%s`\n", f.Fix))
	case f.Fix != "":
		b.WriteString(fmt.Sprintf("      Run: `%s`\n", f.Fix))
	}
	if f.Note != "" {
		b.WriteString(fmt.Sprintf("      Note: %s\n", f.Note))
	}
}

// IssueCount is the number of issue-disposition findings.
func (r Report) IssueCount() int {
	n := 0
	for _, f := range r.Findings {
		if f.Disposition == DispositionIssue {
			n++
		}
	}
	return n
}
