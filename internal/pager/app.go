package pager

import (
	"context"
	"encoding/json"
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
	"github.com/megascan/open-grok/internal/claudeimport"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
	"github.com/megascan/open-grok/internal/workspace"
)

// Options configure the interactive session.
type Options struct {
	CWD       string
	Model     catalog.Model
	YOLO      bool
	Resume    string
	Continue  bool
	Dashboard bool
}

type (
	turnDone    struct{ err error }
	streamDelta struct{ ev agent.Event }
	permAskMsg  struct {
		Tool, Access, Path, Args, Command, Server string
		Reply                                     chan bool
	}
	btwDeltaMsg struct{ text string }
	btwDoneMsg  struct{ err error }
	qAskMsg     struct {
		Question  string
		Options   []string
		Questions []questionItem
		Index     int
		Reply     chan string
		Kind      string
		Directive string
	}
	planAskMsg struct {
		Plan    string
		HasPlan bool
		Reply   chan string
	}
	loopTickMsg struct{ id string }
	queuedFlush struct{}
)

type screen int

const (
	screenWelcome screen = iota
	screenLogin
	screenChat
	screenDashboard
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
	overlayTimeline // /jump picker (live-scrolls transcript)
	overlayQuestion
	overlayFiles
	overlayPlan
	overlayCancelTurn
	overlayWorktree
	overlayMemory
	overlayRewind
	overlayBtw
	overlayBlock
	overlayShortcuts
	overlayGoal
	overlayLineView
	overlayVoice
	overlayLogin
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
	Header             bool
}

type loopJob struct {
	ID      string
	Prompt  string
	Every   time.Duration
	Until   time.Time
	Next    time.Time
	stopped bool
	ticking bool
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
	paletteSrc                                                            []listItem
	loginP                                                                types.Provider
	loginKimiCode                                                         bool
	keyBuf                                                                string
	echoOn                                                                bool
	extTab                                                                int
	docsIdx                                                               int
	confirmKind, resumePending                                            string
	confirmCursor                                                         int
	resumeFilter                                                          resumeSourceFilter
	feedback                                                              string
	perm                                                                  *permAskMsg
	permQueue                                                             []permAskMsg
	permParked                                                            bool
	permCursor                                                            int
	permWordN                                                             int
	permExpanded                                                          bool
	ctxHover                                                              bool
	permFollowup                                                          string
	permFollowupMode                                                      bool
	permPattern                                                           string
	permPatternMode                                                       bool
	permMCPServer                                                         bool
	voice                                                                 *voiceState
	workflows                                                             []workflowRun
	wfDetail, wfPhase                                                     int
	wfActiveID                                                            string
	todos                                                                 []tools.TodoItem
	rewindPhase                                                           rewindPhase
	rewindPoints                                                          []rewindPoint
	rewindCursor                                                          int
	rewindTarget                                                          int
	rewindPreview                                                         string
	btw                                                                   *btwState
	btwDraft                                                              string
	blockTitle                                                            string
	blockBody                                                             string
	blockScroll                                                           int
	usageTab                                                              int
	shortcutExpand                                                        int
	gitBranch                                                             string
	gitWorktree                                                           bool
	question                                                              *qAskMsg
	qCursor                                                               int
	qFree                                                                 string
	qTyping                                                               bool
	qSel                                                                  []int
	qMulti                                                                []map[int]struct{}
	qFrees                                                                []string
	qFreeSel                                                              []bool
	qCursors                                                              []int
	doctorReport                                                          string
	suggest                                                               promptSuggestion
	plan                                                                  *planAskMsg
	planFocus                                                             string
	planScroll                                                            int
	planDraft                                                             string
	planComments                                                          []string
	cancel                                                                *cancelTurnState
	cancelCursor                                                          int
	turnCancel                                                            context.CancelFunc
	alwaysStopKids, alwaysKeepKids                                        bool
	focusScroll                                                           bool
	vimMode, timestamps, compactUI, multiline, mouseOn, thinkingOn, swarm bool
	rememberMode, settingsFilter, autoModeGate                            bool
	modeBanner                                                            string
	settingsEnumKey, settingsEnumOrig                                     string
	settingsEnumIdx                                                       int
	settingsSecretKey, settingsSecret                                     string
	themePreviewOrig                                                      string
	permMode                                                              permMode
	queue                                                                 []string
	loops                                                                 []loopJob
	history                                                               []string
	histIdx                                                               int
	dashCWD                                                               string
	scrollHUD                                                             bool
	fpsOn                                                                 bool
	fpsSamples                                                            []time.Duration
	fpsBody                                                               string
	fpsRefresh                                                            time.Time
	lastViewAt                                                            time.Time
	scrollLogPath                                                         string
	turnStarted                                                           time.Time
	phaseStarted                                                          time.Time
	turnActivity                                                          string
	turnToolTitle                                                         string
	jumpRestore                                                           int
	jumpRestoreSet                                                        bool
	stopHover                                                             bool
	railHover                                                             timelineHit
	transcriptTop                                                         int
	turnStatusY                                                           int
	privacyY                                                              int
	stopBtnX, stopBtnW                                                    int
	lastRail                                                              *timelineRail
	fastTier                                                              bool
	copied                                                                string
	skillCWD                                                              string
	selectedBlock                                                         int
	dashRows                                                              []dashRow
	dashSearch                                                            bool
	dashFilter, dashDraft, dashRename                                     string
	renaming                                                              bool
	dashArmed                                                             time.Time
	fromDash                                                              screen
	followUp, codeMode, imageGen                                          string
	dashGroup                                                             string
	dashIdleExpand                                                        bool
	dashDetails                                                           bool
	dashListFocus                                                         bool
	dashNewAgent                                                          bool
	peekReply                                                             string
	peekFocus                                                             bool
	peekQSel                                                              int
	inactiveCollapsed                                                     bool
	pageFlip, groupTools, simpleMode                                      bool
	chips                                                                 []string
	expandedGroups                                                        map[string]bool
	escArmed                                                              time.Time
	copyToast                                                             string
	headerH, promptY                                                      int
	trustPending                                                          bool
	claudeDismissed                                                       bool
	worktreeLabel                                                         string
	importPlan                                                            claudeimport.Plan
	importSel                                                             []bool
	mem                                                                   *memoryModal
	agents                                                                *agentsModal
	lineView                                                              *lineViewer
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
		opts:              opt,
		cfg:               cfg,
		status:            "ready",
		theme:             themeName,
		screenMd:          cfg.ScreenModeName(),
		vimMode:           cfg.UI.VimMode,
		timestamps:        cfg.UI.Timestamps,
		compactUI:         cfg.UI.CompactMode,
		multiline:         cfg.UI.Multiline,
		mouseOn:           cfg.MouseReportingOn(),
		thinkingOn:        cfg.ShowThinkingOn(),
		swarm:             cfg.UI.SwarmMode,
		dashCWD:           opt.CWD,
		skillCWD:          opt.CWD,
		echoOn:            false,
		followUp:          cfg.UI.FollowUp,
		codeMode:          cfg.UI.CodeMode,
		imageGen:          cfg.UI.ImageGen,
		selectedBlock:     -1,
		peekQSel:          -1,
		inactiveCollapsed: true,
		pageFlip:          cfg.PrefBool("page_flip_on_send", cfg.UI.PageFlipOnSend, true),
		groupTools:        cfg.PrefBool("group_tool_verbs", cfg.UI.GroupToolVerbs, true),
		simpleMode:        cfg.PrefBool("simple_mode", cfg.UI.SimpleMode, true),
		expandedGroups:    map[string]bool{},
		wfDetail:          -1,
		suggest:           promptSuggestion{enabled: promptSuggestionsEnabled(cfg)},
		fpsOn:             envFlagOn("ENTROPY_FPS", "GROK_FPS"),
		scrollHUD:         envFlagOn("ENTROPY_SCROLL_DEBUG", "GROK_SCROLL_DEBUG"),
		railHover:         timelineHitNone,
		autoModeGate:      true,
	}
	if path, ok := envScrollLogStart(); ok {
		m.scrollLogPath = path
	}
	if m.followUp == "" {
		m.followUp = "queue"
	}
	if m.codeMode == "" {
		m.codeMode = "direct"
	}
	if m.imageGen == "" {
		m.imageGen = "grok"
	}
	if opt.YOLO {
		m.permMode = permAlways
	}
	ta := textarea.New()
	ta.Placeholder = "Message Entropy…  (type / for commands)"
	ta.Focus()
	ta.Prompt = promptArrow
	ta.CharLimit = 32 * 1024
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	m.ta = ta
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(colCyan))
	m.spin = sp

	skipWelcome := opt.Resume != "" || opt.Continue
	if opt.Dashboard {
		m.screen = screenDashboard
		m.refreshDashboard()
	} else if skipWelcome {
		if err := m.openSession(opt); err != nil {
			return nil, err
		}
		m.screen = screenChat
		if !m.simpleMode && strings.TrimSpace(m.ta.Value()) == "" {
			m.focusScroll = true
			m.ta.Blur()
		}
	} else {
		m.screen = screenWelcome
		m.refreshWelcome()
	}
	if workspace.ShouldPrompt(opt.CWD) {
		m.trustPending = true
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

func (m *model) attachSession(sess *agent.Session) tea.Cmd {
	m.sess = sess
	m.sess.YOLO = m.permMode == permAlways
	m.sess.PermMode = m.permModeName()
	m.blocks = nil
	m.appendBlock(block{kind: blockSystem, body: "Entropy " + version.String() + "  ·  native Go  ·  " + m.opts.Model.Provider.DisplayName() + " / " + m.opts.Model.Name})
	m.appendBlock(block{kind: blockSystem, body: "State: " + config.Home() + "  ·  session " + sess.ID})
	if snap := sess.Goal.Snapshot(); snap != nil {
		m.appendBlock(block{kind: blockGoal, body: "restored goal (" + string(snap.Status) + "):\n" + snap.Objective + "\n\nActive goals pause across restart. /goal resume to continue."})
	}
	m.rebuildHistory()
	m.todos = tools.LoadTodos(sess.Dir)
	m.screen = screenChat
	m.overlay = overlayNone
	return m.reloadLoops()
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
		cmd := m.applyStream(msg.ev)
		m.refreshVP()
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case turnDone:
		var elapsed *time.Duration
		if !m.turnStarted.IsZero() {
			d := time.Since(m.turnStarted)
			elapsed = &d
		}
		m.busy = false
		m.turnCancel = nil
		m.turnActivity = ""
		m.turnToolTitle = ""
		m.turnStarted = time.Time{}
		m.phaseStarted = time.Time{}
		m.status = "ready"
		m.layoutChat()
		m.finishWorkflow(msg.err)
		m.appendSessionEvent(sessionEventFromTurn(msg.err, elapsed))
		m.refreshVP()
		if msg.err != nil {
			m.suggest.clear()
		}
		if m.screen == screenDashboard {
			m.refreshDashboard()
		}
		if len(m.queue) > 0 && !m.busy {
			next := m.queue[0]
			m.queue = m.queue[1:]
			return m, m.submit(next)
		}
		if m.shouldFetchSuggestion(msg.err) {
			return m, m.fetchPromptSuggestion()
		}
	case recapDoneMsg:
		m.applyRecapDone(msg)
	case suggestionLoadedMsg:
		m.suggest.onLoaded(msg.text, msg.generation)
	case permAskMsg:
		if m.perm != nil {
			m.permQueue = append(m.permQueue, msg)
			break
		}
		m.showPerm(msg)
	case btwDeltaMsg:
		if m.btw != nil && (m.btw.Phase == btwLoading || m.btw.Phase == btwDone) {
			m.btw.Answer += msg.text
		}
	case btwDoneMsg:
		if m.btw != nil {
			if msg.err != nil {
				m.btw.Phase = btwError
				m.btw.Err = msg.err.Error()
			} else {
				m.btw.Phase = btwDone
			}
		}
	case qAskMsg:
		m.question = &msg
		m.qSel = nil
		m.ensureQuestionState()
		if m.overlay != overlayPermission && m.overlay != overlayCancelTurn {
			m.overlay = overlayQuestion
		}
	case planAskMsg:
		m.plan = &msg
		m.planFocus = "preview"
		m.planScroll = 0
		m.planDraft = ""
		m.planComments = nil
		if m.overlay != overlayPermission && m.overlay != overlayCancelTurn {
			m.overlay = overlayPlan
		}
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
	case tea.MouseMsg:
		if cmd, handled := m.handleMouse(msg); handled {
			return m, cmd
		}
	case tea.KeyMsg:
		if cmd, handled := m.handleKey(msg); handled {
			return m, cmd
		}
	}
	livePrompt := m.screen == screenChat && !m.focusScroll && m.perm == nil &&
		(m.overlay == overlayNone || m.isPromptOverlay())
	if livePrompt {
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		cmds = append(cmds, cmd)
		val := strings.TrimSpace(m.ta.Value())
		if m.overlay == overlayHistory {
			m.refreshHistorySearch()
			m.syncPromptPrefix()
		} else if strings.HasPrefix(val, "/") {
			opening := m.overlay != overlayPalette
			m.overlay = overlayPalette
			m.filter = m.ta.Value()
			if opening {
				m.cursor = 0
			}
			m.syncPromptPrefix()
		} else if _, ok := parseAtToken(m.ta.Value()); ok {
			m.refreshFileSearch()
			m.syncPromptPrefix()
		} else if m.isPromptOverlay() {
			m.overlay = overlayNone
			m.syncPromptPrefix()
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
	if m.overlay == overlayVoice {
		return m.handleVoiceKey(msg), true
	}
	if isVoiceChord(msg) && m.voiceKeybindOn() {
		return m.toggleVoice(), true
	}
	if m.overlay == overlayWorkflows {
		return m.handleWorkflowsKey(msg), true
	}
	if m.overlay == overlayShortcuts {
		return m.handleShortcutsKey(msg), true
	}
	if m.overlay == overlayGoal {
		return m.handleGoalOverlayKey(msg), true
	}
	if m.overlay == overlayUsage {
		return m.handleUsageKey(msg), true
	}
	if m.overlay == overlayMemory {
		return m.handleMemoryKey(msg), true
	}
	if m.overlay == overlayAgents {
		return m.handleAgentsKey(msg), true
	}
	if m.overlay == overlayLineView {
		return m.handleLineViewerKey(msg), true
	}
	if m.overlay == overlayRewind {
		return m.handleRewindKey(msg), true
	}
	if m.overlay == overlayBtw {
		return m.handleBtwComposerKey(msg), true
	}
	if m.overlay == overlayBlock {
		return m.handleBlockViewerKey(msg), true
	}
	if m.btw != nil && m.overlay == overlayNone {
		if cmd, handled := m.handleBtwPanelKey(msg); handled {
			return cmd, true
		}
	}
	if m.overlay == overlayPermission && m.perm != nil && !m.permParked {
		return m.handlePermissionKey(msg), true
	}
	if m.overlay == overlayCancelTurn && m.cancel != nil {
		return m.handleCancelTurnKey(msg), true
	}
	if m.overlay == overlayQuestion && m.question != nil {
		return m.handleQuestionKey(msg), true
	}
	if m.overlay == overlayPlan && m.plan != nil {
		return m.handlePlanKey(msg), true
	}
	if m.overlay == overlayShortcuts {
		return m.handleShortcutsKey(msg), true
	}
	if m.overlay == overlayGoal {
		return m.handleGoalOverlayKey(msg), true
	}
	if m.overlay == overlayUsage {
		return m.handleUsageKey(msg), true
	}
	if m.overlay == overlayQueue {
		if cmd, handled := m.handleQueueKey(msg); handled {
			return cmd, true
		}
	}
	if msg.String() == "ctrl+p" && m.screen == screenChat {
		return m.toggleCommandPalette(), true
	}
	if m.overlay != overlayNone && !m.isPromptOverlay() {
		return m.handleOverlayKey(msg)
	}
	if m.screen == screenWelcome {
		return m.handleWelcomeKey(msg), true
	}
	if m.screen == screenLogin {
		return m.handleLoginKey(msg), true
	}
	if m.screen == screenDashboard {
		return m.handleDashboardKey(msg), true
	}
	if m.overlay == overlayPalette {
		return m.handlePaletteKey(msg)
	}
	if m.overlay == overlayFiles {
		return m.handleFileSearchKey(msg)
	}
	if m.overlay == overlayHistory {
		return m.handleHistoryKey(msg)
	}
	if m.screen != screenChat {
		return nil, false
	}
	if m.busy && msg.Type == tea.KeyCtrlC {
		return m.requestCancelTurn(), true
	}
	switch msg.String() {
	case "ctrl+c":
		if m.busy {
			return m.requestCancelTurn(), true
		}
		if m.rememberMode && m.ta.Value() == "" {
			m.exitRememberMode()
			return nil, true
		}
		if m.ta.Value() == "" {
			return tea.Quit, true
		}
		m.ta.Reset()
		return nil, true
	case "esc":
		if m.rememberMode && strings.TrimSpace(m.ta.Value()) == "" {
			m.exitRememberMode()
			return nil, true
		}
		if m.question != nil && m.overlay == overlayNone {
			m.overlay = overlayQuestion
			return nil, true
		}
		if m.perm != nil && m.permParked {
			m.permParked = false
			m.overlay = overlayPermission
			return nil, true
		}
		if m.dashDetails && strings.TrimSpace(m.ta.Value()) == "" && !m.focusScroll {
			m.dashDetails = false
			m.openDashboard()
			return nil, true
		}
		if m.busy {
			if m.vimMode && m.screenMd != modeMinimal {
				return nil, true
			}
			return m.requestCancelTurn(), true
		}
		if m.ta.Value() != "" {
			if !m.escArmed.IsZero() && time.Since(m.escArmed) < 800*time.Millisecond {
				m.ta.Reset()
				m.escArmed = time.Time{}
				return nil, true
			}
			m.escArmed = time.Now()
			m.status = "Esc again to clear"
			return nil, true
		}
		if m.suggest.ghostFor("") != "" {
			m.suggest.dismiss()
			return nil, true
		}
		return nil, true
	case "ctrl+d":
		return tea.Quit, true
	case "ctrl+\\":
		if m.dashDetails {
			m.dashDetails = false
			m.openDashboard()
			return nil, true
		}
		m.openDashboard()
		return nil, true
	case "ctrl+l":
		if cmd, ok := m.tryOpenLineViewerFromDraft(); ok {
			return cmd, true
		}
	case ":":
		if cmd, ok := m.tryOpenLineViewerFromDraft(); ok {
			return cmd, true
		}
		return nil, false
	case "ctrl+x", "ctrl+.":
		m.openShortcuts()
		return nil, true
	case "g":
		if m.focusScroll {
			m.openGoalDetail()
			return nil, true
		}
	case "?":
		if m.focusScroll {
			return m.toggleCommandPalette(), true
		}
	case "[", "]":
		if m.dashDetails {
			delta := 1
			if msg.String() == "[" {
				delta = -1
			}
			return m.cycleDashDetails(delta), true
		}
	case "left", "right", "ctrl+right":
		if msg.String() == "ctrl+right" {
			if m.tryAcceptSuggestion(true) {
				return nil, true
			}
			return nil, true
		}
		if msg.String() == "right" && m.suggest.ghostFor(m.ta.Value()) != "" && !m.focusScroll {
			if m.tryAcceptSuggestion(false) {
				return nil, true
			}
		}
		if m.dashDetails && m.focusScroll {
			delta := 1
			if msg.String() == "left" {
				delta = -1
			}
			return m.cycleDashDetails(delta), true
		}
	case "shift+tab":
		m.cyclePermMode()
		return nil, true
	case "f2", "ctrl+,":
		m.openSettings()
		return nil, true
	case "#":
		if m.canEnterRememberFromHash() {
			m.enterRememberMode()
			return nil, true
		}
		return nil, false
	case "backspace", "ctrl+w", "ctrl+u":
		if m.rememberMode && strings.TrimSpace(m.ta.Value()) == "" {
			m.exitRememberMode()
			return nil, true
		}
	case "tab":
		if m.tryAcceptSuggestion(false) {
			return nil, true
		}
		if m.perm != nil && m.permParked {
			m.permParked = false
			m.overlay = overlayPermission
			return nil, true
		}
		if m.question != nil && m.overlay == overlayNone {
			m.overlay = overlayQuestion
			m.focusScroll = false
			return nil, true
		}
		m.focusScroll = !m.focusScroll
		if m.focusScroll {
			m.ta.Blur()
			if len(m.blocks) > 0 {
				m.selectedBlock = len(m.blocks) - 1
			}
			m.refreshVP()
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
		if m.rememberMode {
			m.ta.Reset()
			m.exitRememberMode()
			if text == "" {
				m.appendSys("Please provide a memory note.")
				m.refreshVP()
				return nil, true
			}
			return m.remember(text), true
		}
		if text == "" {
			return nil, true
		}
		m.ta.Reset()
		m.suggest.clear()
		m.overlay = overlayNone
		if strings.HasPrefix(text, "/") {
			return m.handleSlash(text), true
		}
		if m.busy {
			m.queue = append(m.queue, text)
			kind := "queued follow-up"
			if m.followUp == "steer" {
				kind = "steer queued (injects at next safe gap)"
			}
			m.appendBlock(block{kind: blockSystem, body: kind + " (" + strconv.Itoa(len(m.queue)) + ")"})
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
		return titleStyle.Render("Entropy…")
	}
	var body string
	switch m.screen {
	case screenWelcome:
		body = m.viewWelcome()
	case screenLogin:
		body = m.viewLogin()
	case screenDashboard:
		body = m.viewDashboard()
	default:
		body = m.viewChat()
	}
	if m.overlay != overlayNone {
		body = m.viewWithOverlay(body)
	}
	if m.fpsOn {
		now := time.Now()
		if !m.lastViewAt.IsZero() {
			m.recordFPS(now.Sub(m.lastViewAt))
		}
		m.lastViewAt = now
		panel := m.renderFPSPanel()
		if panel != "" {
			x := max(0, m.width-fpsPanelWidth)
			body = placeOverlay(body, panel, x, 0, m.width, m.height)
		}
	}
	return body
}

func (m *model) layoutChat() {
	header := 3
	footer := 7
	if m.compactUI {
		header, footer = 2, 5
	}
	if m.showPrivacyBanner() {
		header += strings.Count(privacyBanner(), "\n")
	}
	if m.showAnnouncementBanner() {
		header += strings.Count(announcementBanner(), "\n")
	}
	if m.turnStatusVisible() {
		footer++
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

func (m *model) syncPromptPrefix() {
	m.ta.Prompt = m.promptPrefix()
}

func (m *model) persistUI() {
	m.cfg.UI.Theme = m.theme
	m.cfg.UI.VimMode = m.vimMode
	m.cfg.UI.Timestamps = m.timestamps
	m.cfg.UI.CompactMode = m.compactUI
	m.cfg.UI.Multiline = m.multiline
	m.cfg.UI.ScreenMode = m.screenMd
	m.cfg.UI.SwarmMode = m.swarm
	m.cfg.UI.PageFlipOnSend = m.pageFlip
	m.cfg.UI.GroupToolVerbs = m.groupTools
	m.cfg.UI.SimpleMode = m.simpleMode
	m.cfg.SetPrefBool("page_flip_on_send", m.pageFlip)
	m.cfg.SetPrefBool("group_tool_verbs", m.groupTools)
	m.cfg.SetPrefBool("simple_mode", m.simpleMode)
	on := m.mouseOn
	m.cfg.UI.MouseReporting = &on
	th := m.thinkingOn
	m.cfg.UI.ShowThinking = &th
	m.cfg.UI.CodeMode = m.codeMode
	m.cfg.UI.FollowUp = m.followUp
	m.cfg.UI.ImageGen = m.imageGen
	m.cfg.UI.RememberApprovals = m.cfg.UI.RememberApprovals
	m.cfg.Models.Recap = m.cfg.UI.RecapModel
	m.cfg.Models.Memory = m.cfg.UI.MemoryModel
	if m.cfg.Prefs == nil {
		m.cfg.Prefs = config.Prefs{}
	}
	_ = config.Save(m.cfg)
}

func (m *model) applyStream(ev agent.Event) tea.Cmd {
	switch ev.Kind {
	case "text":
		m.setTurnActivity("responding", "")
		if last := m.lastBlock(blockAgent); last != nil {
			last.body += ev.Text
			return nil
		}
		m.appendBlock(block{kind: blockAgent, body: ev.Text, at: time.Now()})
	case "thinking":
		m.setTurnActivity("thinking", "")
		if !m.thinkingOn {
			return nil
		}
		if last := m.lastBlock(blockThinking); last != nil {
			last.body += ev.Text
			return nil
		}
		m.appendBlock(block{kind: blockThinking, body: ev.Text, at: time.Now()})
	case "tool":
		if strings.EqualFold(ev.Tool, "todo_write") {
			m.reloadTodos()
			return nil
		}
		if isSchedulerTool(ev.Tool) {
			return m.reloadLoops()
		}
		kind := blockTool
		title := strings.ToLower(ev.Tool)
		if strings.Contains(title, "image") || strings.EqualFold(ev.Tool, "imagine") {
			kind = blockImage
		}
		if strings.Contains(title, "mermaid") {
			kind = blockMermaid
		}
		m.setTurnActivity("tool", ev.Tool)
		m.appendBlock(block{kind: kind, title: "▸ " + ev.Tool, body: truncate(ev.Text, 400), at: time.Now()})
	case "tool_result":
		if strings.EqualFold(ev.Tool, "todo_write") {
			m.reloadTodos()
			return nil
		}
		if isSchedulerTool(ev.Tool) {
			return m.reloadLoops()
		}
		kind := blockToolResult
		if strings.EqualFold(ev.Tool, "update_goal") {
			kind = blockGoal
		}
		m.setTurnActivity("thinking", "")
		m.appendBlock(block{kind: kind, title: ev.Tool, body: ev.Text, err: ev.IsError, at: time.Now()})
	case "compact_started":
		m.setTurnActivity("compacting", "")
		m.appendSessionEvent(sessionEvent{kind: sessCompactionStarted, percentage: ev.Percent})
	case "compact_completed":
		before := ev.TokensBefore
		ms := ev.ElapsedMS
		m.appendSessionEvent(sessionEvent{
			kind:         sessCompactionCompleted,
			tokensBefore: &before,
			tokensAfter:  ev.TokensAfter,
			elapsedMS:    &ms,
		})
		m.setTurnActivity("thinking", "")
	case "compact_failed":
		m.appendSessionEvent(sessionEvent{kind: sessCompactionFailed, error: ev.Text})
		m.setTurnActivity("thinking", "")
	}
	return nil
}

func isSchedulerTool(name string) bool {
	switch strings.ToLower(name) {
	case "scheduler_create", "scheduler_delete", "scheduler_list":
		return true
	default:
		return false
	}
}

func (m *model) reloadTodos() {
	if m.sess == nil {
		m.todos = nil
		return
	}
	m.todos = tools.LoadTodos(m.sess.Dir)
}

func (m *model) submit(text string) tea.Cmd {
	display, wire := expandSubmit(text)
	return m.submitDisplay(display, wire)
}

func (m *model) submitDisplay(display, wire string) tea.Cmd {
	if m.sess == nil {
		m.appendSys("no active session")
		return nil
	}
	m.history = append(m.history, display)
	m.chips = chipPathsIn(display)
	userIdx := len(m.blocks)
	m.appendBlock(block{kind: blockUser, body: display, at: time.Now()})
	m.appendBlock(block{kind: blockAgent, body: "", at: time.Now()})
	m.busy = true
	m.status = "Thinking…"
	m.turnActivity = "thinking"
	m.turnToolTitle = ""
	now := time.Now()
	m.turnStarted = now
	m.phaseStarted = now
	m.layoutChat()
	m.refreshVP()
	if m.pageFlip {
		m.scrollToBlock(userIdx)
	}
	sess := m.sess
	model := m.opts.Model
	if sess.Effort != "" {
		model.ReasoningEffort = sess.Effort
	}
	kimiCode := model.EnvKey == "KIMI_CODE_API_KEY"
	prog := m.prog
	sess.YOLO = m.permMode == permAlways
	sess.PermMode = m.permModeName()
	ctx, cancel := context.WithCancel(context.Background())
	m.turnCancel = cancel
	return func() tea.Msg {
		defer cancel()
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
			cmd := ""
			for _, k := range []string{"command", "cmd", "script"} {
				if s, ok := parsed[k].(string); ok && s != "" {
					cmd = s
					break
				}
			}
			server := ""
			if mcp, ok := t.(tools.MCPTool); ok {
				server = mcp.ServerName
			}
			if prog != nil {
				prog.Send(permAskMsg{
					Tool: t.Name(), Access: string(t.Access()), Path: path,
					Args: string(args), Command: cmd, Server: server, Reply: ch,
				})
			} else {
				return false
			}
			ok := <-ch
			return ok
		}
		rt.AskUser = func(q string, opts []string) string {
			ch := make(chan string, 1)
			msg := qAskMsg{Question: q, Options: opts, Reply: ch}
			if qs, ok := tools.DecodeAskUserJSON(q); ok {
				for _, item := range qs {
					msg.Questions = append(msg.Questions, questionItem{Question: item.Question, Options: item.Options, Multi: item.Multi})
				}
				if len(msg.Questions) > 0 {
					msg.Question = msg.Questions[0].Question
					msg.Options = msg.Questions[0].Options
				}
			}
			if prog != nil {
				prog.Send(msg)
			} else {
				return ""
			}
			return <-ch
		}
		rt.AskPlan = func(plan string) string {
			ch := make(chan string, 1)
			if prog != nil {
				prog.Send(planAskMsg{
					Plan: plan, HasPlan: strings.TrimSpace(plan) != "" && !strings.HasPrefix(plan, "# No plan written yet"),
					Reply: ch,
				})
			} else {
				return "approve"
			}
			return <-ch
		}
		err = agent.RunTurn(ctx, sess, rt, wire, func(ev agent.Event) {
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
	// Shift+Tab ring: Ask → Plan → Auto → Always-approve → Ask.
	// Auto is skipped when autoModeGate is off (Rust auto_mode_gate).
	inPlan := m.permMode == permPlan || (m.sess != nil && m.sess.Plan.Active)
	inAuto := m.permMode == permAuto
	inYolo := m.permMode == permAlways || (m.sess != nil && m.sess.YOLO)
	switch {
	case !inPlan && !inAuto && !inYolo:
		m.armPlanMode()
		m.showModeBanner("Plan")
	case inPlan && !inAuto && !inYolo:
		m.disarmPlanMode()
		if m.autoModeGate {
			m.permMode = permAuto
			if m.sess != nil {
				m.sess.YOLO = false
			}
			m.showModeBanner("Auto")
		} else {
			m.permMode = permAlways
			if m.sess != nil {
				m.sess.YOLO = true
			}
			m.showModeBanner("Always-Approve")
		}
	case !inPlan && inAuto && !inYolo:
		m.permMode = permAlways
		if m.sess != nil {
			m.sess.YOLO = true
		}
		m.showModeBanner("Always-Approve")
	default:
		m.disarmPlanMode()
		m.permMode = permAsk
		if m.sess != nil {
			m.sess.YOLO = false
		}
		m.showModeBanner("Normal")
	}
	if m.sess != nil {
		m.sess.PermMode = m.permModeName()
	}
}

func (m *model) armPlanMode() {
	m.permMode = permPlan
	if m.sess != nil {
		m.sess.Plan.Enter(m.sess.Plan.PlanFile)
		m.sess.YOLO = false
		m.sess.PermMode = m.permModeName()
	}
}

func (m *model) disarmPlanMode() {
	if m.sess != nil {
		m.sess.Plan.Exit()
	}
}

func (m *model) showModeBanner(name string) {
	m.modeBanner = "Switched to mode: " + name
	m.status = name
}

func (m *model) rememberModeEnabled() bool {
	if m.cfg.Prefs == nil {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(m.cfg.Prefs["features.remember_mode"]))
	return v == "true" || v == "on" || v == "1"
}

func (m *model) enterRememberMode() {
	m.rememberMode = true
	m.ta.Reset()
	m.syncPromptPrefix()
}

func (m *model) exitRememberMode() {
	m.rememberMode = false
	m.syncPromptPrefix()
}

func (m *model) canEnterRememberFromHash() bool {
	return m.rememberModeEnabled() && !m.rememberMode &&
		strings.TrimSpace(m.ta.Value()) == "" &&
		m.overlay == overlayNone && !m.focusScroll
}

func sandboxActive() bool {
	return config.FirstEnv("ENTROPY_SANDBOX", "OPENGROK_SANDBOX", "GROK_SANDBOX") != ""
}
