// Package pager is the Bubble Tea TUI for Open Grok.
package pager

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/version"
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
	spin   spinner.Model
	prog   *tea.Program
	ready  bool
	busy   bool
	status string
	width  int
	height int
	blocks []block
}

// Run starts the interactive TUI.
func Run(ctx context.Context, opt Options) error {
	m, err := newModel(opt)
	if err != nil {
		return err
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx), tea.WithMouseCellMotion())
	m.prog = p
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
	ta.Placeholder = "Message Open Grok…  (/goal  /help)"
	ta.Focus()
	ta.Prompt = "▎ "
	ta.CharLimit = 32 * 1024
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(colCyan))
	m := &model{opts: opt, sess: sess, ta: ta, spin: sp, status: "ready"}
	m.appendBlock(block{kind: blockSystem, body: "Open Grok " + version.String() + "  ·  native Go  ·  " + opt.Model.Provider.DisplayName() + " / " + opt.Model.Name})
	m.appendBlock(block{kind: blockSystem, body: "State: " + config.Home() + "  ·  session " + sess.ID})
	if snap := sess.Goal.Snapshot(); snap != nil {
		m.appendBlock(block{kind: blockGoal, body: "restored goal (" + string(snap.Status) + "):\n" + snap.Objective + "\n\nActive goals pause across restart. /goal resume to continue."})
	} else {
		m.appendBlock(block{kind: blockSystem, body: "Set an autonomous goal with /goal <objective>. The rail on the right tracks it."})
	}
	return m, nil
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spin.Tick)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		header := 3
		footer := 7
		h := msg.Height - header - footer
		if h < 4 {
			h = 4
		}
		vw := m.transcriptWidth()
		if !m.ready {
			m.vp = viewport.New(vw, h)
			m.ready = true
		} else {
			m.vp.Width = vw
			m.vp.Height = h
		}
		m.ta.SetWidth(max(20, msg.Width-6))
		m.refreshVP()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
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
		m.applyStream(msg.ev)
		m.refreshVP()
	case turnDone:
		m.busy = false
		m.status = "ready"
		if msg.err != nil {
			m.appendBlock(block{kind: blockSystem, body: "error: " + msg.err.Error(), err: true})
			m.refreshVP()
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
		return titleStyle.Render("Open Grok…")
	}
	head := m.renderHeader()
	body := m.vp.View()
	if m.showSidebar() {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.vp.View(), m.renderSidebar())
	}
	input := inputBox.Width(max(20, m.width-2)).Render(m.ta.View())
	return head + "\n" + barStyle.Render(strings.Repeat("─", max(8, m.width))) + "\n" +
		body + "\n" + input + "\n" + m.renderFooter()
}

func (m *model) applyStream(ev agent.Event) {
	switch ev.Kind {
	case "text":
		if last := m.lastBlock(blockAgent); last != nil {
			last.body += ev.Text
			return
		}
		m.appendBlock(block{kind: blockAgent, body: ev.Text})
	case "thinking":
		if last := m.lastBlock(blockThinking); last != nil {
			last.body += ev.Text
			return
		}
		m.appendBlock(block{kind: blockThinking, body: ev.Text})
	case "tool":
		title := ev.Tool
		if strings.EqualFold(ev.Tool, "update_goal") {
			title = "update_goal"
		}
		m.appendBlock(block{kind: blockTool, title: "▸ " + title, body: truncate(ev.Text, 400)})
	case "tool_result":
		kind := blockToolResult
		if strings.EqualFold(ev.Tool, "update_goal") {
			kind = blockGoal
		}
		m.appendBlock(block{kind: kind, title: ev.Tool, body: ev.Text, err: ev.IsError})
	}
}

func (m *model) submit(text string) tea.Cmd {
	m.appendBlock(block{kind: blockUser, body: text})
	m.appendBlock(block{kind: blockAgent, body: ""})
	m.busy = true
	m.status = "sampling"
	m.refreshVP()
	sess := m.sess
	model := m.opts.Model
	kimiCode := model.EnvKey == "KIMI_CODE_API_KEY"
	prog := m.prog
	return func() tea.Msg {
		bearer, _, err := auth.ResolveBearer(model.Provider, auth.EnvKeys(model.Provider), kimiCode)
		if err != nil {
			return turnDone{err: err}
		}
		err = agent.RunTurn(context.Background(), sess, agent.Runtime{Model: model, Bearer: bearer}, text, func(ev agent.Event) {
			if prog != nil {
				prog.Send(streamDelta{ev: ev})
			}
		})
		return turnDone{err: err}
	}
}
