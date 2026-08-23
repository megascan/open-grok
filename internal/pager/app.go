// Package pager is the Bubble Tea TUI for Open Grok.
package pager

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/version"
)

var (
	titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	userStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("219")).Bold(true)
	agentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("51"))
	toolStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	barStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
)

// Options configure the interactive session.
type Options struct {
	CWD      string
	Model    catalog.Model
	YOLO     bool
	Resume   string
	Continue bool
}

type turnDone struct {
	err error
}

type streamDelta struct {
	ev agent.Event
}

type model struct {
	opts   Options
	sess   *agent.Session
	vp     viewport.Model
	ta     textarea.Model
	ready  bool
	busy   bool
	status string
	log    strings.Builder
	width  int
	height int
	slash  []string
}

// Run starts the interactive TUI.
func Run(ctx context.Context, opt Options) error {
	m, err := newModel(opt)
	if err != nil {
		return err
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err = p.Run()
	return err
}

func newModel(opt Options) (*model, error) {
	var sess *agent.Session
	var err error
	if opt.Continue {
		list, _ := agent.ListSessions(opt.CWD)
		if len(list) > 0 {
			s := list[0]
			sess = &s
		}
	}
	if opt.Resume != "" {
		sess, err = agent.LoadSession(opt.CWD, opt.Resume)
		if err != nil {
			return nil, err
		}
	}
	if sess == nil {
		sess, err = agent.NewSession(opt.CWD, opt.Model, opt.YOLO)
		if err != nil {
			return nil, err
		}
	}
	sess.YOLO = opt.YOLO
	ta := textarea.New()
	ta.Placeholder = "Message Open Grok…  (/help for commands)"
	ta.Focus()
	ta.Prompt = "▎ "
	ta.CharLimit = 32 * 1024
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	m := &model{opts: opt, sess: sess, ta: ta, status: "ready"}
	m.append(dimStyle.Render("Open Grok " + version.String() + "  ·  native Go  ·  " + opt.Model.Provider.DisplayName() + " / " + opt.Model.Name))
	m.append(dimStyle.Render("State: " + config.Home() + "  ·  session " + sess.ID))
	m.append("")
	return m, nil
}

func (m *model) Init() tea.Cmd { return textarea.Blink }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		header := 3
		footer := 6
		if !m.ready {
			m.vp = viewport.New(msg.Width, msg.Height-header-footer)
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = msg.Height - header - footer
		}
		m.ta.SetWidth(msg.Width - 2)
		m.vp.SetContent(m.log.String())
	case tea.KeyMsg:
		if m.busy {
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
			break
		}
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			if m.ta.Value() == "" {
				return m, tea.Quit
			}
			m.ta.Reset()
		case tea.KeyCtrlD:
			return m, tea.Quit
		case tea.KeyEnter:
			if msg.Alt {
				break
			}
			text := strings.TrimSpace(m.ta.Value())
			if text == "" {
				break
			}
			m.ta.Reset()
			if strings.HasPrefix(text, "/") {
				return m, m.handleSlash(text)
			}
			return m, m.submit(text)
		}
	case streamDelta:
		switch msg.ev.Kind {
		case "text":
			m.appendChunk(agentStyle.Render(msg.ev.Text))
		case "thinking":
			m.appendChunk(dimStyle.Render(msg.ev.Text))
		case "tool":
			m.append(toolStyle.Render("▸ " + msg.ev.Tool))
		case "tool_result":
			body := msg.ev.Text
			if len(body) > 400 {
				body = body[:400] + "…"
			}
			style := dimStyle
			if msg.ev.IsError {
				style = errStyle
			}
			m.append(style.Render(body))
		}
		m.vp.SetContent(m.log.String())
		m.vp.GotoBottom()
	case streamThenDone:
		if msg.text != "" {
			m.append("\n" + agentStyle.Render(msg.text))
		}
		m.busy = false
		m.status = "ready"
		m.vp.SetContent(m.log.String())
		m.vp.GotoBottom()
	case turnDone:
		m.busy = false
		m.status = "ready"
		if msg.err != nil {
			m.append(errStyle.Render("error: " + msg.err.Error()))
			m.vp.SetContent(m.log.String())
		}
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	cmds = append(cmds, cmd)
	m.vp, cmd = m.vp.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *model) View() string {
	if !m.ready {
		return "Open Grok…"
	}
	head := titleStyle.Render(" Open Grok ") + dimStyle.Render(fmt.Sprintf(" %s  %s  %s", version.String(), m.opts.Model.Name, m.status))
	foot := dimStyle.Render(" enter send  ·  ctrl+c quit  ·  /login /model /help")
	return head + "\n" + barStyle.Render(strings.Repeat("─", max(8, m.width))) + "\n" +
		m.vp.View() + "\n" + barStyle.Render(strings.Repeat("─", max(8, m.width))) + "\n" +
		m.ta.View() + "\n" + foot
}

func (m *model) append(line string) {
	if m.log.Len() > 0 {
		m.log.WriteByte('\n')
	}
	m.log.WriteString(line)
}

func (m *model) appendChunk(s string) {
	m.log.WriteString(s)
}

func (m *model) submit(text string) tea.Cmd {
	m.append(userStyle.Render("you") + "  " + text)
	m.append(agentStyle.Render("open-grok"))
	m.busy = true
	m.status = "sampling"
	m.vp.SetContent(m.log.String())
	m.vp.GotoBottom()
	sess := m.sess
	model := m.opts.Model
	kimiCode := model.EnvKey == "KIMI_CODE_API_KEY"
	return func() tea.Msg {
		bearer, _, err := auth.ResolveBearer(model.Provider, auth.EnvKeys(model.Provider), kimiCode)
		if err != nil {
			return turnDone{err: err}
		}
		err = agent.RunTurn(context.Background(), sess, agent.Runtime{Model: model, Bearer: bearer}, text, func(ev agent.Event) {
			// Bubble Tea programs are not thread-safe for Update; stream via commands.
		})
		_ = err
		// Collecting via direct RunTurn then returning one done event keeps the TUI simple
		// and race-free. Token streaming is used in headless/ACP paths.
		if err == nil {
			last := lastAssistant(sess)
			return streamThenDone{text: last}
		}
		return turnDone{err: err}
	}
}

type streamThenDone struct{ text string }

func lastAssistant(s *agent.Session) string {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role == "assistant" {
			return s.Messages[i].Content
		}
	}
	return ""
}

func (m *model) handleSlash(text string) tea.Cmd {
	fields := strings.Fields(text)
	cmd := strings.TrimPrefix(fields[0], "/")
	arg := ""
	if len(fields) > 1 {
		arg = strings.Join(fields[1:], " ")
	}
	switch cmd {
	case "help":
		m.append(dimStyle.Render("/help  /login  /model [id]  /quit  /clear  /yolo  /compact  /plan  /sessions  /export  /memory  /mcp  /status  /fork  /rewind"))
	case "quit", "exit":
		return tea.Quit
	case "clear":
		m.log.Reset()
		m.vp.SetContent("")
	case "yolo":
		m.sess.YOLO = !m.sess.YOLO
		m.append(fmt.Sprintf("always-approve = %v", m.sess.YOLO))
	case "compact":
		_ = m.sess.AppendUpdate(map[string]any{"type": "auto_compact_started", "manual": true})
		m.append("compaction runs automatically when the transcript is large; a checkpoint was recorded")
	case "plan":
		if m.sess.Plan.Active {
			m.sess.Plan.Exit()
			m.append("plan mode off")
		} else {
			m.sess.Plan.Enter(m.sess.Plan.PlanFile)
			m.append("plan mode on — only " + m.sess.Plan.PlanFile + " may be edited")
		}
	case "sessions":
		list, _ := agent.ListSessions(m.opts.CWD)
		for _, s := range list {
			m.append(fmt.Sprintf("%s  %s  %s", s.ID, s.UpdatedAt.Format("2006-01-02 15:04"), s.Title))
		}
	case "export":
		m.append(m.sess.ExportMarkdown())
	case "memory":
		res := memorySearch(arg)
		m.append(res)
	case "mcp":
		cfg, _ := config.Load()
		if len(cfg.MCP) == 0 {
			m.append("(no mcp_servers)")
			break
		}
		for name, s := range cfg.MCP {
			m.append(name + "  " + s.Command)
		}
	case "status":
		m.append(fmt.Sprintf("session %s  model %s  provider %s  yolo %v  plan %v", m.sess.ID, m.opts.Model.ID, m.opts.Model.Provider, m.sess.YOLO, m.sess.Plan.Active))
	case "fork":
		dst, err := agent.ForkSession(m.sess)
		if err != nil {
			m.append(errStyle.Render(err.Error()))
			break
		}
		m.sess = dst
		m.append("forked to " + dst.ID)
	case "rewind":
		if len(m.sess.Messages) > 2 {
			m.sess.Messages = m.sess.Messages[:len(m.sess.Messages)-2]
			_ = m.sess.Persist()
			m.append("rewound last exchange")
		}
	case "model":
		if arg == "" {
			m.append("current model: " + m.opts.Model.ID + " (" + m.opts.Model.Provider.DisplayName() + ")")
			break
		}
		mod, ok := catalog.Lookup(arg)
		if !ok {
			m.append(errStyle.Render("unknown model " + arg))
			break
		}
		m.opts.Model = mod
		m.sess.ModelID = mod.ID
		m.sess.Provider = mod.Provider
		m.sess.Backend = mod.APIBackend
		m.sess.CloseExportBoundary(mod.Provider)
		m.append("switched to " + mod.Name + " via " + mod.Provider.DisplayName())
	case "login":
		m.append("use `open-grok login [provider]` from a TTY to store credentials under ~/.opengrok")
	default:
		m.append(errStyle.Render("unknown command " + cmd))
	}
	m.vp.SetContent(m.log.String())
	m.vp.GotoBottom()
	return nil
}

func memorySearch(q string) string {
	body, _ := json.Marshal(map[string]string{"query": q})
	return tools.MemorySearch{}.Execute(context.Background(), tools.Context{}, body).Content
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
