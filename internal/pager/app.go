package pager

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
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

type (
	turnDone    struct{ err error }
	streamDelta struct{ ev agent.Event }
	permAskMsg  struct {
		Tool, Access, Path, Args string
		Reply                    chan bool
	}
	loopTickMsg struct{ id string }
	queuedFlush struct{}
)

type screen int

const (
	screenWelcome screen = iota
	screenLogin
	screenChat
)

type overlay int

const (
	overlayNone overlay = iota
	overlayPalette
	overlaySettings
	overlayTheme
	overlayDashboard
	overlayResume
	overlayHistory
	overlayDoctor
	overlayDocs
	overlayTutorial
	overlayExtensions
	overlayPermission
	overlayConfirm
	overlayFeedback
	overlayModel
	overlayWorkflows
	overlayPersonas
	overlayAgents
	overlayFind
	overlayHelp
	overlayPrivacy
	overlayAnnouncements
	overlayReleaseNotes
	overlayUsage
	overlayQueue
	overlayTasks
	overlayCache
	overlayImportClaude
	overlayMCP
	overlayTimeline
)

type permMode int

const (
	permAsk permMode = iota
	permPlan
	permAlways
	permAuto
)

type listItem struct {
	Key, Label, Detail string
}

type loopJob struct {
	ID      string
	Prompt  string
	Every   time.Duration
	Until   time.Time
	stopped bool
}

type model struct {
	opts     Options
	cfg      config.File
	sess     *agent.Session
	vp       viewport.Model
	ta       textarea.Model
	spin     spinner.Model
	prog     *tea.Program
	ready    bool
	busy     bool
	status   string
	width    int
	height   int
	blocks   []block
	screen   screen
	overlay  overlay
	theme    string
	screenMd string // fullscreen | minimal

	cursor                                                                int
	filter                                                                string
	items                                                                 []listItem
	loginP                                                                types.Provider
	loginKimiCode                                                         bool
	keyBuf                                                                string
	echoOn                                                                bool
	extTab                                                                int
	docsIdx                                                               int
	confirmKind                                                           string
	feedback                                                              string
	perm                                                                  *permAskMsg
	permParked                                                            bool
	focusScroll                                                           bool
	vimMode, timestamps, compactUI, multiline, mouseOn, thinkingOn, swarm bool
	permMode                                                              permMode
	queue                                                                 []string
	loops                                                                 []loopJob
	history                                                               []string
	histIdx                                                               int
	dashCWD                                                               string
	scrollHUD                                                             bool
	fastTier                                                              bool
	copied                                                                string
	skillCWD                                                              string
}

// Run starts the interactive TUI.
func Run(ctx context.Context, opt Options) error {
	m, err := newModel(opt)
	if err != nil {
		return err
	}
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	if m.mouseOn {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(m, opts...)
	m.prog = p
	_, err = p.Run()
	return err
}

func newModel(opt Options) (*model, error) {
	cfg, _ := config.Load()
	themeName := cfg.UI.Theme
	if themeName == "" {
		themeName = "groknight"
	}
	applyTheme(resolveTheme(themeName))
	m := &model{
		opts:       opt,
		cfg:        cfg,
		status:     "ready",
		theme:      themeName,
		screenMd:   cfg.ScreenModeName(),
		vimMode:    cfg.UI.VimMode,
		timestamps: cfg.UI.Timestamps,
		compactUI:  cfg.UI.CompactMode,
		multiline:  cfg.UI.Multiline,
		mouseOn:    cfg.MouseReportingOn(),
		thinkingOn: cfg.ShowThinkingOn(),
		swarm:      cfg.UI.SwarmMode,
		dashCWD:    opt.CWD,
		skillCWD:   opt.CWD,
		echoOn:     false,
	}
	if opt.YOLO {
		m.permMode = permAlways
	}
	ta := textarea.New()
	ta.Placeholder = "Message Open Grok…  (type / for commands)"
	ta.Focus()
	ta.Prompt = "▎ "
	ta.CharLimit = 32 * 1024
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	m.ta = ta
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(colCyan))
	m.spin = sp

	skipWelcome := opt.Resume != "" || opt.Continue
	if skipWelcome {
		if err := m.openSession(opt); err != nil {
			return nil, err
		}
		m.screen = screenChat
	} else {
		m.screen = screenWelcome
		m.refreshWelcome()
	}
	return m, nil
}

func (m *model) openSession(opt Options) error {
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
			return err
		}
	}
	if sess == nil {
		sess, err = agent.NewSession(opt.CWD, opt.Model, opt.YOLO)
		if err != nil {
			return err
		}
	}
	m.attachSession(sess)
	return nil
}

func (m *model) attachSession(sess *agent.Session) {
	m.sess = sess
	m.sess.YOLO = m.permMode == permAlways
	m.sess.PermMode = m.permModeName()
	m.blocks = nil
	m.appendBlock(block{kind: blockSystem, body: "Open Grok " + version.String() + "  ·  native Go  ·  " + m.opts.Model.Provider.DisplayName() + " / " + m.opts.Model.Name})
	m.appendBlock(block{kind: blockSystem, body: "State: " + config.Home() + "  ·  session " + sess.ID})
	if snap := sess.Goal.Snapshot(); snap != nil {
		m.appendBlock(block{kind: blockGoal, body: "restored goal (" + string(snap.Status) + "):\n" + snap.Objective + "\n\nActive goals pause across restart. /goal resume to continue."})
	}
	m.rebuildHistory()
	m.screen = screenChat
	m.overlay = overlayNone
}

func (m *model) permModeName() string {
	switch m.permMode {
	case permAlways:
		return "always"
	case permAuto:
		return "auto"
	case permPlan:
		return "plan"
	default:
		return "ask"
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spin.Tick)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layoutChat()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
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
		if len(m.queue) > 0 && !m.busy {
			next := m.queue[0]
			m.queue = m.queue[1:]
			return m, m.submit(next)
		}
	case permAskMsg:
		m.perm = &msg
		m.overlay = overlayPermission
		m.permParked = false
	case loopTickMsg:
		return m, m.onLoopTick(msg.id)
	case editDone:
		if msg.err != nil {
			m.appendSys(msg.err.Error())
			m.refreshVP()
			break
		}
		m.ta.SetValue(strings.TrimSpace(msg.text))
		m.ta.Focus()
	case tea.KeyMsg:
		if cmd, handled := m.handleKey(msg); handled {
			return m, cmd
		}
	}
	if m.screen == screenChat && m.overlay == overlayNone && !m.focusScroll && m.perm == nil {
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		cmds = append(cmds, cmd)
		if strings.HasPrefix(strings.TrimSpace(m.ta.Value()), "/") {
			m.overlay = overlayPalette
			m.filter = strings.TrimSpace(m.ta.Value())
			m.cursor = 0
		}
	}
	if m.screen == screenChat && (m.focusScroll || m.overlay == overlayNone) {
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.overlay == overlayPermission && m.perm != nil && !m.permParked {
		return m.handlePermissionKey(msg), true
	}
	if m.overlay != overlayNone && m.overlay != overlayPalette {
		return m.handleOverlayKey(msg)
	}
	if m.screen == screenWelcome {
		return m.handleWelcomeKey(msg), true
	}
	if m.screen == screenLogin {
		return m.handleLoginKey(msg), true
	}
	if m.overlay == overlayPalette {
		return m.handlePaletteKey(msg)
	}
	if m.screen != screenChat {
		return nil, false
	}
	if m.busy && msg.Type == tea.KeyCtrlC {
		return tea.Quit, true
	}
	switch msg.String() {
	case "ctrl+c":
		if m.ta.Value() == "" {
			return tea.Quit, true
		}
		m.ta.Reset()
		return nil, true
	case "esc":
		if m.ta.Value() != "" {
			m.ta.Reset()
			return nil, true
		}
		if m.busy {
			return nil, true
		}
		return tea.Quit, true
	case "ctrl+d":
		return tea.Quit, true
	case "ctrl+\\":
		m.openDashboard()
		return nil, true
	case "shift+tab":
		m.cyclePermMode()
		return nil, true
	case "tab":
		if m.perm != nil && m.permParked {
			m.permParked = false
			m.overlay = overlayPermission
			return nil, true
		}
		m.focusScroll = !m.focusScroll
		if m.focusScroll {
			m.ta.Blur()
		} else {
			m.ta.Focus()
		}
		return nil, true
	case "pgup", "pgdown":
		if msg.String() == "pgup" {
			m.vp.ViewUp()
		} else {
			m.vp.ViewDown()
		}
		return nil, true
	}
	if m.focusScroll {
		return m.handleScrollKey(msg), true
	}
	if msg.Type == tea.KeyEnter {
		if m.multiline && !msg.Alt {
			return nil, false
		}
		if msg.Alt && !m.multiline {
			return nil, false
		}
		text := strings.TrimSpace(m.ta.Value())
		if text == "" {
			return nil, true
		}
		m.ta.Reset()
		m.overlay = overlayNone
		if strings.HasPrefix(text, "/") {
			return m.handleSlash(text), true
		}
		if m.busy {
			m.queue = append(m.queue, text)
			m.appendBlock(block{kind: blockSystem, body: "queued follow-up (" + strconv.Itoa(len(m.queue)) + ")"})
			m.refreshVP()
			return nil, true
		}
		return m.submit(text), true
	}
	if msg.Type == tea.KeyUp && strings.TrimSpace(m.ta.Value()) == "" && len(m.history) > 0 {
		m.openHistory()
		return nil, true
	}
	return nil, false
}

func (m *model) View() string {
	if !m.ready && m.screen == screenChat {
		return titleStyle.Render("Open Grok…")
	}
	var body string
	switch m.screen {
	case screenWelcome:
		body = m.viewWelcome()
	case screenLogin:
		body = m.viewLogin()
	default:
		body = m.viewChat()
	}
	if m.overlay != overlayNone {
		body = m.viewWithOverlay(body)
	}
	return body
}

func (m *model) layoutChat() {
	header := 3
	footer := 7
	if m.compactUI {
		header, footer = 2, 5
	}
	h := m.height - header - footer
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
	m.ta.SetWidth(max(20, m.width-6))
	m.refreshVP()
}

func (m *model) persistUI() {
	m.cfg.UI.Theme = m.theme
	m.cfg.UI.VimMode = m.vimMode
	m.cfg.UI.Timestamps = m.timestamps
	m.cfg.UI.CompactMode = m.compactUI
	m.cfg.UI.Multiline = m.multiline
	m.cfg.UI.ScreenMode = m.screenMd
	m.cfg.UI.SwarmMode = m.swarm
	on := m.mouseOn
	m.cfg.UI.MouseReporting = &on
	th := m.thinkingOn
	m.cfg.UI.ShowThinking = &th
	_ = config.Save(m.cfg)
}

func (m *model) applyStream(ev agent.Event) {
	switch ev.Kind {
	case "text":
		if last := m.lastBlock(blockAgent); last != nil {
			last.body += ev.Text
			return
		}
		m.appendBlock(block{kind: blockAgent, body: ev.Text, at: time.Now()})
	case "thinking":
		if !m.thinkingOn {
			return
		}
		if last := m.lastBlock(blockThinking); last != nil {
			last.body += ev.Text
			return
		}
		m.appendBlock(block{kind: blockThinking, body: ev.Text, at: time.Now()})
	case "tool":
		title := ev.Tool
		m.appendBlock(block{kind: blockTool, title: "▸ " + title, body: truncate(ev.Text, 400), at: time.Now()})
	case "tool_result":
		kind := blockToolResult
		if strings.EqualFold(ev.Tool, "update_goal") {
			kind = blockGoal
		}
		m.appendBlock(block{kind: kind, title: ev.Tool, body: ev.Text, err: ev.IsError, at: time.Now()})
	}
}

func (m *model) submit(text string) tea.Cmd {
	m.history = append(m.history, text)
	m.appendBlock(block{kind: blockUser, body: text, at: time.Now()})
	m.appendBlock(block{kind: blockAgent, body: "", at: time.Now()})
	m.busy = true
	m.status = "sampling"
	m.refreshVP()
	sess := m.sess
	model := m.opts.Model
	if sess.Effort != "" {
		model.ReasoningEffort = sess.Effort
	}
	kimiCode := model.EnvKey == "KIMI_CODE_API_KEY"
	prog := m.prog
	sess.YOLO = m.permMode == permAlways
	sess.PermMode = m.permModeName()
	return func() tea.Msg {
		bearer, _, err := auth.ResolveBearer(model.Provider, auth.EnvKeys(model.Provider), kimiCode)
		if err != nil {
			return turnDone{err: err}
		}
		rt := agent.Runtime{Model: model, Bearer: bearer}
		rt.Approve = func(t tools.Tool, args json.RawMessage) bool {
			ch := make(chan bool, 1)
			path := ""
			var parsed map[string]any
			_ = json.Unmarshal(args, &parsed)
			if p, ok := parsed["path"].(string); ok {
				path = p
			}
			if prog != nil {
				prog.Send(permAskMsg{
					Tool: t.Name(), Access: string(t.Access()), Path: path,
					Args: string(args), Reply: ch,
				})
			} else {
				return false
			}
			ok := <-ch
			return ok
		}
		err = agent.RunTurn(context.Background(), sess, rt, text, func(ev agent.Event) {
			if prog != nil {
				prog.Send(streamDelta{ev: ev})
			}
		})
		return turnDone{err: err}
	}
}

func (m *model) rebuildHistory() {
	m.history = nil
	if m.sess == nil {
		return
	}
	for _, msg := range m.sess.Messages {
		if msg.Role == types.RoleUser {
			m.history = append(m.history, msg.Content)
		}
	}
}

func (m *model) cyclePermMode() {
	switch m.permMode {
	case permAsk:
		m.permMode = permPlan
		if m.sess != nil {
			m.sess.Plan.Enter(m.sess.Plan.PlanFile)
			m.sess.YOLO = false
		}
		m.status = "plan mode"
	case permPlan:
		m.permMode = permAlways
		if m.sess != nil {
			m.sess.Plan.Exit()
			m.sess.YOLO = true
		}
		m.status = "always-approve"
	default:
		m.permMode = permAsk
		if m.sess != nil {
			m.sess.Plan.Exit()
			m.sess.YOLO = false
			m.sess.PermMode = "ask"
		}
		m.status = "ask"
	}
	if m.sess != nil {
		m.sess.PermMode = m.permModeName()
	}
}

func sandboxActive() bool {
	return strings.TrimSpace(os.Getenv("OPENGROK_SANDBOX")) != "" || strings.TrimSpace(os.Getenv("GROK_SANDBOX")) != ""
}
