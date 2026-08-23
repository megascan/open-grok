package pager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
)

func (m *model) handleSlash(text string) tea.Cmd {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return nil
	}
	raw := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	arg := ""
	if len(fields) > 1 {
		arg = strings.Join(fields[1:], " ")
	}
	if cmd, ok := ResolveSlash(raw); ok {
		if cmd.Mode != "" && cmd.Mode != m.screenMd {
			other := "/fullscreen"
			if m.screenMd == modeFull {
				other = "/minimal"
			}
			m.appendSys(fmt.Sprintf("/%s isn't available in %s mode. Run %s to switch this session.", cmd.Name, m.screenMd, other))
			m.refreshVP()
			return nil
		}
		return m.runSlash(cmd.Name, arg)
	}
	if cmd := m.skillSlash(raw, arg); cmd != nil {
		return cmd
	}
	m.appendSys("unknown command /" + raw + "  ·  /help")
	m.refreshVP()
	return nil
}

func (m *model) skillSlash(name, arg string) tea.Cmd {
	if m.sess == nil {
		return nil
	}
	for _, s := range tools.DiscoverSkills(m.opts.CWD) {
		if !s.UserInvocable {
			continue
		}
		if strings.EqualFold(s.Name, name) || strings.EqualFold("local:"+s.Name, name) || strings.EqualFold("user:"+s.Name, name) {
			prompt := "Follow skill " + s.Name + ":\n" + s.Body
			if arg != "" {
				prompt += "\n\nUser request: " + arg
			}
			return m.submit(prompt)
		}
	}
	return nil
}

func (m *model) runSlash(name, arg string) tea.Cmd {
	switch name {
	case "quit":
		return tea.Quit
	case "help":
		m.openHelp()
	case "docs":
		if strings.EqualFold(arg, "web") {
			m.appendSys("https://docs.x.ai/build/overview")
			break
		}
		m.openDocs()
		if arg != "" {
			for i, it := range m.items {
				if strings.Contains(strings.ToLower(it.Label), strings.ToLower(arg)) {
					m.cursor = i
					return m.pickOverlay()
				}
			}
		}
	case "home":
		m.screen = screenWelcome
		m.overlay = overlayNone
		m.refreshWelcome()
	case "delete":
		m.confirmKind = "delete"
		m.overlay = overlayConfirm
	case "new":
		sess, err := agent.NewSession(m.opts.CWD, m.opts.Model, m.permMode == permAlways)
		if err != nil {
			m.appendSys(err.Error())
			break
		}
		m.attachSession(sess)
		m.layoutChat()
	case "fork":
		if m.sess == nil {
			break
		}
		dst, err := agent.ForkSession(m.sess)
		if err != nil {
			m.appendSys(err.Error())
			break
		}
		m.attachSession(dst)
		m.appendSys("forked to " + dst.ID)
	case "compact":
		if m.sess != nil {
			agent.CompactNow(m.sess, arg)
			m.appendSys("compacted" + func() string {
				if arg != "" {
					return " (keep: " + arg + ")"
				}
				return ""
			}())
		}
	case "copy":
		n, path := parseCopyArg(arg)
		if path != "" {
			var body string
			if m.sess != nil {
				body = m.sess.ExportMarkdown()
			}
			if n > 1 {
				return m.copyNthAgent(n)
			}
			if body != "" {
				_ = os.WriteFile(path, []byte(body), 0o600)
				m.appendSys("wrote " + path)
				break
			}
		}
		return m.copyNthAgent(n)
	case "find":
		m.overlay = overlayFind
		m.cursor = 0
		m.items = nil
		q := strings.ToLower(arg)
		for _, b := range m.blocks {
			if q == "" || strings.Contains(strings.ToLower(b.body), q) {
				m.items = append(m.items, listItem{Key: b.body, Label: truncate(b.body, 70)})
			}
		}
	case "history":
		m.openHistory()
	case "export":
		if m.sess == nil {
			break
		}
		md := m.sess.ExportMarkdown()
		if arg != "" {
			_ = os.WriteFile(arg, []byte(md), 0o600)
			m.appendSys("exported " + arg)
			break
		}
		m.appendSys(md)
	case "transcript":
		if m.sess != nil {
			m.appendSys(m.sess.ExportMarkdown())
		}
	case "edit-prompt":
		return m.editPrompt()
	case "expand":
		m.toggleFold(-1)
	case "context":
		m.openUsage(0)
	case "minimal":
		m.screenMd = modeMinimal
		m.persistUI()
		m.appendSys("minimal mode — terminal palette, no theme picker. /fullscreen to switch back.")
	case "fullscreen":
		m.screenMd = modeFull
		m.persistUI()
		m.appendSys("fullscreen mode. /minimal to switch back.")
	case "model":
		if arg == "" {
			m.openModels()
			break
		}
		fields := strings.Fields(arg)
		mod, ok := catalog.Lookup(fields[0])
		if !ok {
			m.appendSys("unknown model " + fields[0])
			break
		}
		m.opts.Model = mod
		if m.sess != nil {
			m.sess.ModelID = mod.ID
			m.sess.Provider = mod.Provider
			m.sess.Backend = mod.APIBackend
			m.sess.CloseExportBoundary(mod.Provider)
		}
		if len(fields) > 1 && m.sess != nil {
			m.sess.Effort = strings.ToLower(fields[1])
		}
		m.appendSys("switched to " + mod.Name + " via " + mod.Provider.DisplayName())
	case "effort":
		if m.sess != nil {
			m.sess.Effort = strings.ToLower(arg)
			m.appendSys("effort = " + m.sess.Effort)
		}
	case "fast":
		m.fastTier = !m.fastTier
		m.appendSys(fmt.Sprintf("codex fast tier = %v", m.fastTier))
	case "always-approve":
		if m.permMode == permAlways {
			m.permMode = permAsk
			if m.sess != nil {
				m.sess.YOLO = false
			}
			m.appendSys("always-approve off")
		} else {
			m.permMode = permAlways
			if m.sess != nil {
				m.sess.YOLO = true
				m.sess.Plan.Exit()
			}
			m.appendSys("always-approve on")
		}
	case "yolo-2":
		if !sandboxActive() {
			m.appendSys("/yolo-2 refuses to run without an applied OS sandbox. Start with ENTROPY_SANDBOX=1.")
			break
		}
		m.permMode = permAlways
		if m.sess != nil {
			m.sess.YOLO = true
		}
		m.appendSys("yolo-2 on (sandbox active)")
	case "auto":
		if m.permMode == permAuto {
			m.permMode = permAsk
			m.appendSys("auto permission mode off")
		} else {
			m.permMode = permAuto
			if m.sess != nil {
				m.sess.YOLO = false
				m.sess.PermMode = "auto"
			}
			m.appendSys("auto permission mode on — bash still asks")
		}
	case "multiline":
		m.multiline = !m.multiline
		m.persistUI()
		m.appendSys("multiline = " + onOff(m.multiline))
	case "compact-mode":
		m.compactUI = !m.compactUI
		m.persistUI()
		m.layoutChat()
		m.appendSys("compact-mode = " + onOff(m.compactUI))
	case "vim-mode":
		m.vimMode = !m.vimMode
		m.persistUI()
		m.appendSys("vim-mode = " + onOff(m.vimMode))
	case "hooks":
		m.openExtensions(0)
	case "plugins":
		m.openExtensions(1)
	case "marketplace":
		m.openExtensions(2)
	case "skills":
		m.openExtensions(3)
	case "mcps":
		m.openMCP()
	case "share":
		if m.sess != nil {
			return m.copyText("Entropy session "+m.sess.ID+" · "+m.opts.Model.Name+"\n"+m.sess.ExportMarkdown(), false)
		}
	case "session-info":
		m.openUsage(2)
	case "rename":
		if m.sess != nil && arg != "" {
			m.sess.Title = arg
			_ = m.sess.Persist()
			m.appendSys("title = " + arg)
		}
	case "dashboard":
		m.openDashboard()
	case "cd":
		if arg == "" {
			m.appendSys("dashboard spawn cwd: " + m.dashCWD)
			break
		}
		abs, err := filepath.Abs(arg)
		if err == nil {
			m.dashCWD = abs
			m.appendSys("new dashboard sessions will spawn in " + abs)
		}
	case "add-dir":
		if m.sess != nil && arg != "" {
			if err := m.sess.AddWorkingDir(arg); err != nil {
				m.appendSys(err.Error())
			} else {
				m.appendSys("added working dir " + arg)
			}
		}
	case "remove-dir":
		if m.sess != nil && arg != "" {
			_ = m.sess.RemoveWorkingDir(arg)
			m.appendSys("removed working dir " + arg)
		}
	case "theme":
		if arg == "" {
			m.openTheme()
			break
		}
		if strings.EqualFold(arg, "next") {
			arg = nextThemeName(m.theme)
		}
		m.theme = arg
		applyTheme(resolveTheme(arg))
		saveTheme(arg)
		m.appendSys("theme: " + resolveTheme(arg).Name)
	case "feedback":
		if arg != "" {
			m.appendSys("feedback recorded locally:\n" + arg)
			break
		}
		m.overlay = overlayFeedback
		m.feedback = ""
	case "announcements":
		m.overlay = overlayAnnouncements
		m.items = []listItem{{Label: "Entropy native Go", Detail: "The shipping binary is entropy. Rust crates remain the behavioral reference."}}
		m.cursor = 0
	case "remember":
		return m.remember(arg)
	case "plan":
		if m.sess == nil {
			break
		}
		if m.sess.Plan.Active && arg == "" {
			m.sess.Plan.Exit()
			m.permMode = permAsk
			m.appendSys("plan mode off")
			break
		}
		m.sess.Plan.Enter(m.sess.Plan.PlanFile)
		m.permMode = permPlan
		m.appendSys("plan mode on — only " + m.sess.Plan.PlanFile + " may be edited")
		if arg != "" {
			_ = os.WriteFile(m.sess.Plan.PlanFile, []byte("# Plan\n\n"+arg+"\n"), 0o600)
			return m.submit("Enter plan mode. Draft the plan in the session plan file. Description: " + arg)
		}
	case "swarm":
		return m.handleSwarm(arg)
	case "view-plan":
		if m.sess == nil {
			break
		}
		data, err := os.ReadFile(m.sess.Plan.PlanFile)
		if err != nil {
			m.appendSys("no plan saved yet")
			break
		}
		m.appendSys(string(data))
	case "resume":
		m.openResume()
	case "workflows":
		m.openWorkflows()
	case "btw":
		if arg == "" {
			m.openBtwComposer("")
			break
		}
		return m.startBTW(arg)
	case "recap":
		return m.submit("Recap this conversation: decisions, open questions, and next steps. Be concise.")
	case "doctor":
		m.openDoctor()
	case "voice":
		m.appendSys("Voice capture is not bound to a microphone in this Go pager. Type instead — the prompt is focused.")
		m.focusScroll = false
		m.ta.Focus()
	case "loop":
		return m.startLoop(arg)
	case "imagine":
		if arg == "" {
			m.appendSys("usage: /imagine <description>")
			break
		}
		return m.submit("Generate an image (tool: image generation if available, otherwise a detailed visual description the user can feed to an image model): " + arg)
	case "imagine-video":
		if arg == "" {
			m.appendSys("usage: /imagine-video <description>")
			break
		}
		return m.submit("Plan shots and describe a video (image_to_video if the provider supports it): " + arg)
	case "timestamps":
		m.timestamps = !m.timestamps
		m.persistUI()
		m.appendSys("timestamps = " + onOff(m.timestamps))
	case "timeline":
		m.overlay = overlayTimeline
		m.cursor = 0
		m.items = nil
		for i, b := range m.blocks {
			if b.kind == blockUser {
				m.items = append(m.items, listItem{Key: strconv.Itoa(i), Label: truncate(b.body, 70)})
			}
		}
	case "toggle-mouse-reporting":
		m.mouseOn = !m.mouseOn
		m.persistUI()
		m.appendSys("mouse reporting = " + onOff(m.mouseOn) + " (takes effect next launch)")
	case "settings":
		m.openSettings()
	case "privacy":
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "opt-in":
			m.ackPrivacyBanner(true)
			m.appendSys("privacy: opted in")
		case "opt-out":
			m.ackPrivacyBanner(false)
			m.appendSys("privacy: opted out")
		default:
			m.overlay = overlayPrivacy
			m.cursor = 0
			m.items = []listItem{
				{Key: "opt-in", Label: "Opt in", Detail: "allow coding-data retention/training where the provider offers it"},
				{Key: "opt-out", Label: "Opt out", Detail: "do not share coding data for training"},
			}
		}
	case "rewind":
		m.openRewindPicker()
	case "jump":
		m.overlay = overlayTimeline
		m.cursor = 0
		m.items = nil
		for i, b := range m.blocks {
			if b.kind == blockUser {
				m.items = append(m.items, listItem{Key: strconv.Itoa(i), Label: truncate(b.body, 70)})
			}
		}
	case "login":
		m.openLogin(arg)
	case "logout":
		p := m.opts.Model.Provider
		if arg != "" {
			if parsed, ok := types.ParseProvider(arg); ok {
				p = parsed
			}
		}
		_ = auth.Logout(p, strings.EqualFold(arg, "all"))
		m.appendSys("signed out of " + p.DisplayName())
		m.screen = screenLogin
		m.openLogin("")
	case "import-claude":
		m.openImportClaude()
	case "usage":
		m.openUsage(1)
	case "cache":
		m.overlay = overlayCache
		n := 0
		if m.sess != nil {
			for _, msg := range m.sess.Messages {
				n += len(msg.Content)
			}
		}
		m.items = []listItem{{Label: "prompt bytes in session", Detail: strconv.Itoa(n)}}
		m.cursor = 0
	case "queue":
		if strings.EqualFold(arg, "clear") {
			m.queue = nil
			m.appendSys("queue cleared")
			m.overlay = overlayNone
			break
		}
		m.openQueue()
	case "tasks":
		m.overlay = overlayTasks
		m.items = nil
		for _, j := range m.loops {
			st := j.Every.String()
			if j.stopped {
				st = "stopped"
			}
			m.items = append(m.items, listItem{Label: j.ID, Detail: st + "  " + truncate(j.Prompt, 40)})
		}
		if len(m.items) == 0 {
			m.items = []listItem{{Label: "(no background loops)"}}
		}
		m.cursor = 0
	case "release-notes":
		m.openReleaseNotes()
	case "tutorial":
		m.openTutorial()
	case "config-agents":
		m.overlay = overlayAgents
		m.items = dirListing(append([]string{filepath.Join(config.Home(), "agents")}, config.ProjectSubdir(m.opts.CWD, "agents")...)...)
		m.cursor = 0
	case "personas":
		m.overlay = overlayPersonas
		m.items = dirListing(append([]string{filepath.Join(config.Home(), "personas")}, config.ProjectSubdir(m.opts.CWD, "personas")...)...)
		m.cursor = 0
	case "gboom":
		m.appendSys("💥 gboom")
	case "scroll-debug":
		m.scrollHUD = !m.scrollHUD
	case "debug":
		m.appendSys(fmt.Sprintf("debug overlay: screen=%s theme=%s vim=%v yolo=%s busy=%v", m.screenMd, m.theme, m.vimMode, m.permModeName(), m.busy))
	case "goal":
		return m.handleGoal(arg)
	case "memory":
		return m.handleMemory(arg)
	case "flush":
		return m.submit("Flush this session into memory: write the durable facts with memory_save.")
	case "dream":
		return m.submit("Dream/consolidate existing memory notes into organized topics using memory_search then memory_save.")
	case "deep-research":
		if arg == "" {
			m.appendSys("usage: /deep-research <query>")
			break
		}
		return m.submit("Deep-research workflow: plan bounded questions, gather sourced claims, cross-check, and report only surviving claims with locators. Query: " + arg)
	case "ultracode":
		if arg == "" {
			m.appendSys("usage: /ultracode <task>")
			break
		}
		return m.submit("Ultracode workflow: scout, competing plans, implement, adversarial review. Task: " + arg)
	case "workflow":
		return m.handleWorkflow(arg)
	default:
		m.appendSys("unhandled /" + name)
	}
	m.refreshVP()
	return nil
}

func (m *model) sessionInfo() string {
	if m.sess == nil {
		return "No active session"
	}
	bytes := 0
	turns := 0
	for _, msg := range m.sess.Messages {
		bytes += len(msg.Content)
		if msg.Role == types.RoleUser {
			turns++
		}
	}
	return fmt.Sprintf(
		"session %s\ntitle %s\nmodel %s (%s)\nprovider %s\nbackend %s\nyolo %v\nplan %v\nmode %s\n%d user turns · %d bytes\nworking dirs: %s\n%s\nEntropy %s",
		m.sess.ID, m.sess.Title, m.opts.Model.ID, m.opts.Model.Name, m.opts.Model.Provider, m.opts.Model.APIBackend,
		m.sess.YOLO, m.sess.Plan.Active, m.screenMd, turns, bytes, strings.Join(m.sess.ExtraDirs, ", "),
		m.sess.Goal.StatusLine(), version.String(),
	)
}

func (m *model) contextReport() string {
	if m.sess == nil {
		return "no session"
	}
	sys, user, asst, tool := 0, 0, 0, 0
	for _, msg := range m.sess.Messages {
		n := len(msg.Content)
		switch msg.Role {
		case types.RoleSystem:
			sys += n
		case types.RoleUser:
			user += n
		case types.RoleAssistant:
			asst += n
		case types.RoleTool:
			tool += n
		}
	}
	win := m.opts.Model.ContextWindow
	if win == 0 {
		win = 128_000
	}
	used := sys + user + asst + tool
	free := win*4 - used
	if free < 0 {
		free = 0
	}
	return fmt.Sprintf("context (~bytes, 4 bytes/token estimate of %d window)\nsystem %d\nmessages %d\nassistant/reasoning %d\ntools %d\nfree ~%d",
		win, sys, user+asst, asst, tool, free)
}

func (m *model) remember(arg string) tea.Cmd {
	if arg == "" {
		m.appendSys("usage: /remember <note>")
		m.refreshVP()
		return nil
	}
	body, _ := json.Marshal(map[string]string{"name": "note-" + strconv.FormatInt(time.Now().Unix(), 10), "content": arg})
	res := tools.MemorySave{}.Execute(context.Background(), tools.Context{}, body)
	m.appendSys(res.Content)
	m.refreshVP()
	return nil
}

func (m *model) handleMemory(arg string) tea.Cmd {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "on":
		m.cfg.Features.Memory = true
		_ = config.Save(m.cfg)
		m.appendSys("memory on")
	case "off":
		m.cfg.Features.Memory = false
		_ = config.Save(m.cfg)
		m.appendSys("memory off")
	default:
		if !m.cfg.MemoryEnabled() {
			m.appendSys("memory is off — /memory on or ENTROPY_MEMORY=1")
			break
		}
		m.overlay = overlayMemory
		m.cursor = 0
		m.items = nil
		dir := filepath.Join(config.Home(), "memory")
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			if arg != "" && !strings.Contains(strings.ToLower(e.Name()+string(data)), strings.ToLower(arg)) {
				continue
			}
			m.items = append(m.items, listItem{Key: e.Name(), Label: e.Name(), Detail: truncate(string(data), 80)})
		}
		if len(m.items) == 0 {
			body, _ := json.Marshal(map[string]string{"query": arg})
			res := tools.MemorySearch{}.Execute(context.Background(), tools.Context{}, body)
			m.items = []listItem{{Label: res.Content}}
		}
	}
	m.refreshVP()
	return nil
}

func (m *model) handleSwarm(arg string) tea.Cmd {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "", "toggle":
		m.swarm = !m.swarm
	case "on":
		m.swarm = true
	case "off":
		m.swarm = false
	default:
		m.swarm = true
		if m.sess != nil {
			m.sess.Swarm = true
		}
		m.persistUI()
		return m.submit("Swarm mode: partition this task across parallel subagents (depth 1), then merge. Task: " + arg)
	}
	if m.sess != nil {
		m.sess.Swarm = m.swarm
	}
	m.persistUI()
	m.appendSys("swarm = " + onOff(m.swarm))
	m.refreshVP()
	return nil
}

func (m *model) handleGoal(arg string) tea.Cmd {
	if m.sess == nil {
		return nil
	}
	gs := parseGoalSlash(arg)
	switch gs.Verb {
	case goalVerbStatus:
		m.openGoalDetail()
	case goalVerbPause:
		if m.sess.Goal.PauseUser() {
			_ = m.sess.AppendUpdate(map[string]any{"type": "goal_mode_state", "status": "user_paused"})
			m.appendBlock(block{kind: blockGoal, body: "goal paused"})
		} else {
			m.appendBlock(block{kind: blockGoal, body: "nothing to pause (goal is not Active)", err: true})
		}
	case goalVerbResume:
		if m.sess.Goal.Resume() {
			_ = m.sess.AppendUpdate(map[string]any{"type": "goal_mode_state", "status": "active"})
			m.appendBlock(block{kind: blockGoal, body: "goal resumed — Active"})
			m.refreshVP()
			return m.submit("Continue the active goal. Call update_goal as you make progress.")
		}
		m.appendBlock(block{kind: blockGoal, body: "nothing to resume", err: true})
	case goalVerbComplete:
		if m.sess.Goal.UserComplete() {
			_ = m.sess.AppendUpdate(map[string]any{"type": "goal_mode_state", "status": "complete"})
			m.appendBlock(block{kind: blockGoal, body: "goal marked complete (user-verified)"})
		} else {
			m.appendBlock(block{kind: blockGoal, body: "no goal to complete", err: true})
		}
	case goalVerbClear:
		m.sess.Goal.Clear()
		_ = m.sess.AppendUpdate(map[string]any{"type": "goal_mode_state", "status": "cleared"})
		m.appendBlock(block{kind: blockGoal, body: "goal cleared"})
	case goalVerbStart:
		if gs.Objective == "" {
			m.appendBlock(block{kind: blockGoal, body: m.goalStatusText()})
			break
		}
		if err := m.sess.Goal.Create(gs.Objective, gs.Budget); err != nil {
			m.appendBlock(block{kind: blockGoal, body: err.Error(), err: true})
			break
		}
		_ = m.sess.AppendUpdate(map[string]any{
			"type": "goal_mode_state", "status": "active", "objective": gs.Objective,
		})
		m.appendBlock(block{kind: blockGoal, body: "Active goal:\n" + gs.Objective + "\n\nThe model reports progress with update_goal. Completion is a claim until you run /goal complete."})
		m.refreshVP()
		return m.submit("Begin the active goal. Inspect the workspace, make progress, and call update_goal after meaningful steps.")
	}
	m.refreshVP()
	return nil
}

func (m *model) goalStatusText() string {
	snap := m.sess.Goal.Snapshot()
	if snap == nil {
		return "No goal. Start one with /goal <objective> [--budget <tokens>]."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "status: %s\nphase: %s\nid: %s\n\n%s", snap.Status, snap.Phase, snap.GoalID, snap.Objective)
	if snap.CompletionClaimed {
		fmt.Fprintf(&b, "\n\ncompletion claim pending /goal complete:\n%s", snap.CompletionSummary)
	}
	if snap.PauseMessage != "" {
		fmt.Fprintf(&b, "\n\npause: %s", snap.PauseMessage)
	}
	return b.String()
}

func (m *model) editPrompt() tea.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	tmp, err := os.CreateTemp("", "entropy-prompt-*.md")
	if err != nil {
		m.appendSys(err.Error())
		return nil
	}
	_ = tmp.Close()
	c := exec.Command(editor, tmp.Name())
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return tea.ExecProcess(c, func(err error) tea.Msg {
		data, _ := os.ReadFile(tmp.Name())
		_ = os.Remove(tmp.Name())
		return editDone{text: string(data), err: err}
	})
}

type editDone struct {
	text string
	err  error
}

func (m *model) openWorkflows() {
	m.overlay = overlayWorkflows
	m.cursor = 0
	m.items = dirListing(append(config.ProjectSubdir(m.opts.CWD, "workflows"), filepath.Join(config.Home(), "workflows"))...)
}

func (m *model) handleWorkflow(arg string) tea.Cmd {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		m.openWorkflows()
		return nil
	}
	switch fields[0] {
	case "pause", "resume", "stop", "save":
		m.appendSys("workflow " + fields[0] + " " + strings.Join(fields[1:], " ") + " — runs are tracked in /workflows")
		return nil
	}
	name := fields[0]
	rest := strings.Join(fields[1:], " ")
	for _, dir := range append(config.ProjectSubdir(m.opts.CWD, "workflows"), filepath.Join(config.Home(), "workflows")) {
		p := filepath.Join(dir, name)
		for _, ext := range []string{"", ".rhai", ".md"} {
			data, err := os.ReadFile(p + ext)
			if err != nil {
				continue
			}
			return m.submit("Launch saved workflow " + name + " with args " + rest + ".\nScript:\n" + string(data))
		}
	}
	return m.submit("Launch workflow " + name + " " + rest)
}

func (m *model) startLoop(arg string) tea.Cmd {
	every, prompt := parseLoop(arg)
	if prompt == "" {
		m.appendSys("usage: /loop [interval] <prompt>   e.g. /loop 30m check deploy")
		return nil
	}
	if every < time.Minute {
		every = time.Minute
	}
	id := fmt.Sprintf("loop-%d", time.Now().Unix()%100000)
	job := loopJob{ID: id, Prompt: prompt, Every: every, Until: time.Now().Add(7 * 24 * time.Hour)}
	m.loops = append(m.loops, job)
	m.appendSys("loop " + id + " every " + every.String() + " (expires in 7 days)")
	return tea.Tick(every, func(t time.Time) tea.Msg { return loopTickMsg{id: id} })
}

func (m *model) onLoopTick(id string) tea.Cmd {
	var job *loopJob
	for i := range m.loops {
		if m.loops[i].ID == id {
			job = &m.loops[i]
			break
		}
	}
	if job == nil || job.stopped || time.Now().After(job.Until) {
		return nil
	}
	cmd := m.submit("[loop " + id + "] " + job.Prompt)
	return tea.Batch(cmd, tea.Tick(job.Every, func(t time.Time) tea.Msg { return loopTickMsg{id: id} }))
}

func parseLoop(arg string) (time.Duration, string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0, ""
	}
	fields := strings.Fields(arg)
	if d, err := time.ParseDuration(fields[0]); err == nil {
		return d, strings.TrimSpace(strings.TrimPrefix(arg, fields[0]))
	}
	return time.Hour, arg
}

func (m *model) usageItems() []listItem {
	st := auth.Status()
	var items []listItem
	for _, p := range types.AllProviders {
		mark := "signed out — usage unavailable"
		if s, ok := st[p]; ok {
			mark = s + " · quota is provider-hosted (open the provider console)"
		}
		items = append(items, listItem{Label: p.DisplayName(), Detail: mark})
	}
	return items
}
