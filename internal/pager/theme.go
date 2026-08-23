package pager

import (
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/config"
)

// Theme is one of the five built-in palettes plus auto.
type Theme struct {
	Name        string
	Aliases     []string
	Cyan        string
	Pink        string
	Gold        string
	Red         string
	Green       string
	Muted       string
	Bar         string
	Panel       string
	Claim       string
	HeaderBg    string
	HeaderFg    string
	InputBorder string
	Truecolor   bool
}

var builtinThemes = []Theme{
	{
		Name: "groknight", Aliases: []string{"grok-night", "dark"},
		Cyan: "51", Pink: "219", Gold: "214", Red: "203", Green: "114",
		Muted: "244", Bar: "238", Panel: "236", Claim: "220",
		HeaderBg: "17", HeaderFg: "231", InputBorder: "63",
	},
	{
		Name: "grokday", Aliases: []string{"grok-day", "light", "day"},
		Cyan: "25", Pink: "162", Gold: "130", Red: "160", Green: "28",
		Muted: "243", Bar: "250", Panel: "254", Claim: "94",
		HeaderBg: "189", HeaderFg: "17", InputBorder: "25",
	},
	{
		Name: "tokyonight", Aliases: []string{"tokyo-night", "tokyo"},
		Cyan: "#7dcfff", Pink: "#bb9af7", Gold: "#e0af68", Red: "#f7768e", Green: "#9ece6a",
		Muted: "#565f89", Bar: "#3b4261", Panel: "#1a1b26", Claim: "#ff9e64",
		HeaderBg: "#24283b", HeaderFg: "#c0caf5", InputBorder: "#7aa2f7",
		Truecolor: true,
	},
	{
		Name: "rosepine", Aliases: []string{"rose-pine", "rosepine-moon", "rose-pine-moon"},
		Cyan: "#9ccfd8", Pink: "#c4a7e7", Gold: "#f6c177", Red: "#eb6f92", Green: "#3e8fb0",
		Muted: "#6e6a86", Bar: "#393552", Panel: "#232136", Claim: "#ea9a97",
		HeaderBg: "#2a273f", HeaderFg: "#e0def4", InputBorder: "#c4a7e7",
		Truecolor: true,
	},
	{
		Name: "oscura", Aliases: []string{"oscura-midnight"},
		Cyan: "#c4b5fd", Pink: "#f0abfc", Gold: "#fbbf24", Red: "#fb7185", Green: "#4ade80",
		Muted: "#64748b", Bar: "#1e1b4b", Panel: "#0f0a1a", Claim: "#facc15",
		HeaderBg: "#1e1b4b", HeaderFg: "#e2e8f0", InputBorder: "#a78bfa",
		Truecolor: true,
	},
}

func resolveTheme(name string) Theme {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || n == "auto" || n == "system" {
		if systemDark() {
			return resolveTheme("groknight")
		}
		return resolveTheme("grokday")
	}
	for _, t := range builtinThemes {
		if t.Name == n {
			return t
		}
		for _, a := range t.Aliases {
			if a == n {
				return t
			}
		}
	}
	return builtinThemes[0]
}

func nextThemeName(current string) string {
	cur := resolveTheme(current).Name
	for i, t := range builtinThemes {
		if t.Name == cur {
			return builtinThemes[(i+1)%len(builtinThemes)].Name
		}
	}
	return builtinThemes[0].Name
}

func applyTheme(t Theme) {
	colCyan, colPink, colGold, colRed, colGreen = t.Cyan, t.Pink, t.Gold, t.Red, t.Green
	colMuted, colBar, colPanel, colClaim = t.Muted, t.Bar, t.Panel, t.Claim
	rebuildStyles(t)
}

func rebuildStyles(t Theme) {
	titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Cyan)).Bold(true)
	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted))
	userStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Pink)).Bold(true)
	agentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Cyan)).Bold(true)
	toolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Gold))
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Red))
	okStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Green)).Bold(true)
	claimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Claim)).Bold(true)
	barStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Bar))
	userLabel = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Pink)).Bold(true).PaddingRight(1)
	agentLabel = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Cyan)).Bold(true).PaddingRight(1)
	sysLabel = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted)).Italic(true)
	codeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("237"))
	headMd = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Cyan)).Bold(true)
	bulletMd = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Gold))
	goalTitle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Green)).Bold(true)
	panelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(t.InputBorder)).Padding(0, 1)
	toolCard = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(t.Gold)).Padding(0, 1)
	goalCard = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(t.Green)).Padding(0, 1)
	errCard = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(t.Red)).Padding(0, 1)
	inputBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(t.InputBorder)).Padding(0, 1)
	headerBar = lipgloss.NewStyle().Background(lipgloss.Color(t.HeaderBg)).Foreground(lipgloss.Color(t.HeaderFg)).Bold(true).Padding(0, 1)
	modalBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(t.Cyan)).Padding(1, 2).Background(lipgloss.Color(t.Panel))
	selStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.HeaderFg)).Background(lipgloss.Color(t.InputBorder)).Padding(0, 1)
}

func systemDark() bool {
	if runtime.GOOS == "darwin" {
		return true
	}
	if v := os.Getenv("GTK_THEME"); strings.Contains(strings.ToLower(v), "dark") {
		return true
	}
	if os.Getenv("COLORFGBG") == "0;15" {
		return false
	}
	return true
}

func saveTheme(name string) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	cfg.UI.Theme = name
	_ = config.Save(cfg)
}
