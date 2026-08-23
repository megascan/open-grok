package pager

import "github.com/charmbracelet/lipgloss"

var (
	colCyan    = "51"
	colPink    = "219"
	colGold    = "214"
	colRed     = "203"
	colGreen   = "114"
	colMuted   = "244"
	colBar     = "238"
	colPanel   = "236"
	colClaim   = "220"
	colUserBg  = "53"
	colAgentBg = "23"
)

var (
	titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colCyan)).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	userStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colPink)).Bold(true)
	agentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colCyan)).Bold(true)
	toolStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colGold))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colRed))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colGreen)).Bold(true)
	claimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colClaim)).Bold(true)
	barStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colBar))
	userLabel  = lipgloss.NewStyle().Foreground(lipgloss.Color(colPink)).Bold(true).PaddingRight(1)
	agentLabel = lipgloss.NewStyle().Foreground(lipgloss.Color(colCyan)).Bold(true).PaddingRight(1)
	sysLabel   = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Italic(true)
	codeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("237"))
	headMd     = lipgloss.NewStyle().Foreground(lipgloss.Color(colCyan)).Bold(true)
	bulletMd   = lipgloss.NewStyle().Foreground(lipgloss.Color(colGold))
	goalTitle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colGreen)).Bold(true)
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
	toolCard = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colGold)).
			Padding(0, 1)
	goalCard = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colGreen)).
			Padding(0, 1)
	errCard = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colRed)).
		Padding(0, 1)
	inputBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
	headerBar = lipgloss.NewStyle().
			Background(lipgloss.Color("17")).
			Foreground(lipgloss.Color("231")).
			Bold(true).
			Padding(0, 1)
	modalBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("51")).
			Padding(1, 2).
			Background(lipgloss.Color("236"))
	selStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("63")).
			Padding(0, 1)
)
