package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveFixID(t *testing.T) {
	for _, in := range []string{"ssh-wrap", "terminal.ssh-wrap", "tmux-clipboard", "terminal.dcs-passthrough"} {
		if _, err := ResolveFixID(in); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
	}
	if _, err := ResolveFixID("nope"); err == nil {
		t.Fatal("unknown must fail")
	}
}

func TestFormatReportLayout(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("TMUX", "")
	rep := Collect()
	text := Format(rep)
	for _, want := range []string{"Environment\n", "  terminal     ", "  multiplexer  ", "  ssh          ", "Clipboard\n", "  native       ", "  osc 52       ", "Voice\n", "  microphone   "} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
}

func TestApplyTmuxClipboardFix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BYOBU", "")
	t.Setenv("BYOBU_BACKEND", "")
	t.Setenv("BYOBU_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	plan, err := ApplyFix(TmuxClipboardID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(plan.Path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, TmuxClipboardLine) {
		t.Fatalf("missing line in %s:\n%s", plan.Path, body)
	}
	if !strings.Contains(body, "# >>> terminal.tmux-clipboard >>>") {
		t.Fatalf("missing managed markers:\n%s", body)
	}
	if !strings.Contains(plan.SuccessMessage(), "Added `set -g set-clipboard on`") {
		t.Fatalf("success: %s", plan.SuccessMessage())
	}
	again, err := ApplyFix(TmuxClipboardID)
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyOK {
		t.Fatal("second apply should be already healthy")
	}
}

func TestHumanFixCommand(t *testing.T) {
	if HumanFixCommand(SSHWrapID) != "entropy doctor fix ssh-wrap" {
		t.Fatal(HumanFixCommand(SSHWrapID))
	}
}

func TestUnknownDoctorArgs(t *testing.T) {
	if Usage != "Usage: /doctor [fix [ssh-wrap|tmux-clipboard|dcs-passthrough|tmux-extended-keys]]" {
		t.Fatal(Usage)
	}
}
