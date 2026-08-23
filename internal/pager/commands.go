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
		if cmd.Name == "minimal" && m.screenMd == modeMinimal {
			m.appendSys("You're already in minimal mode.")
			m.refreshVP()
			return nil
		}
		if cmd.Name == "fullscreen" && m.screenMd == modeFull {
			m.appendSys("You're already in fullscreen mode.")
			m.refreshVP()
			return nil
		}
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
		if m.sess == nil {
			m.appendSys("No active session to delete")
			break
		}
		m.confirmKind = "delete"
		m.overlay = overlayConfirm
	case "new":
		return m.handleNew()
	case "fork":
		return m.handleFork(arg)
	case "compact":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		agent.CompactNow(m.sess, arg)
		m.appendSys("compacted" + func() string {
			if arg != "" {
				return " (keep: " + arg + ")"
			}
			return ""
		}())
	case "copy":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		n, path, err := parseCopyArg(arg)
		if err != nil {
			m.appendSys(err.Error())
			break
		}
		return m.copyAssistant(n, path)
	case "find":
		return m.openFind(arg)
	case "history":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		m.openHistory()
	case "export":
		return m.exportConversation(arg)
	case "transcript":
		return m.openTranscriptPager()
	case "edit-prompt":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		return m.editPrompt()
	case "expand":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		m.toggleFold(-1)
	case "context":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		m.openUsage(0)
	case "minimal":
		if m.sess == nil {
			m.appendSys("No active session to reopen in minimal mode")
			break
		}
		m.screenMd = modeMinimal
		m.persistUI()
		m.appendSys("minimal mode — terminal palette, no theme picker. /fullscreen to switch back.")
	case "fullscreen":
		if m.sess == nil {
			m.appendSys("No active session to reopen in fullscreen mode")
			break
		}
		m.screenMd = modeFull
		m.persistUI()
		m.appendSys("fullscreen mode. /minimal to switch back.")
	case "model":
		if arg == "" {
			m.appendSys("Usage: /model <name> [effort]")
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
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		if msg, ok := applyEffort(m.sess, arg); !ok {
			m.appendSys(msg)
		} else {
			m.appendSys("effort = " + m.sess.Effort)
		}
	case "fast":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		if strings.TrimSpace(m.opts.Model.ID) == "" && strings.TrimSpace(m.opts.Model.Name) == "" {
			m.appendSys("No active model")
			break
		}
		if !m.opts.Model.SupportsFast() {
			m.appendSys("current model does not support Fast mode")
			break
		}
		m.fastTier = !m.fastTier
		m.appendSys(saveSuccessToast("Fast mode", m.fastTier))
	case "always-approve":
		m.toggleAlwaysApprove()
	case "yolo-2":
		if !sandboxActive() {
			m.appendSys(yolo2SandboxError)
			break
		}
		m.toggleAlwaysApprove()
	case "auto":
		currentlyAuto := m.permMode == permAuto
		if currentlyAuto {
			m.permMode = permAsk
			if m.sess != nil {
				m.sess.YOLO = false
				m.sess.PermMode = "ask"
			}
			m.appendSys(permissionModeToast(permAsk))
		} else {
			m.permMode = permAuto
			if m.sess != nil {
				m.sess.YOLO = false
				m.sess.PermMode = "auto"
			}
			m.appendSys(permissionModeToast(permAuto))
		}
	case "multiline":
		m.multiline = !m.multiline
		m.persistUI()
		m.appendSys(saveSuccessToast("Multiline", m.multiline))
	case "compact-mode":
		m.compactUI = !m.compactUI
		m.persistUI()
		m.layoutChat()
		m.appendSys(saveSuccessToast("Compact mode", m.compactUI))
	case "vim-mode":
		m.vimMode = !m.vimMode
		m.persistUI()
		m.appendSys(saveSuccessToast("Vim scrollback", m.vimMode))
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
		if m.sess == nil {
			m.appendSys("No active session to share")
			break
		}
		return m.copyText("Entropy session "+m.sess.ID+" · "+m.opts.Model.Name+"\n"+m.sess.ExportMarkdown(), false)
	case "session-info":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		m.openUsage(2)
	case "rename":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		title, auto, errMsg := parseRenameArg(arg)
		if errMsg != "" {
			m.appendSys(errMsg)
			break
		}
		if auto {
			m.sess.Title = ""
			_ = m.sess.Persist()
			m.appendSys("title reset to auto")
			break
		}
		m.sess.Title = title
		_ = m.sess.Persist()
		m.appendSys("title = " + title)
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
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		if strings.TrimSpace(arg) == "" {
			m.appendSys("Usage: /add-dir <path>")
			break
		}
		if err := m.sess.AddWorkingDir(arg); err != nil {
			if os.IsNotExist(err) || strings.Contains(err.Error(), "not a directory") {
				m.appendSys("Not a directory: " + strings.TrimSpace(arg))
			} else {
				m.appendSys(err.Error())
			}
		} else {
			m.appendSys("Adding working directory…")
		}
	case "remove-dir":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		if strings.TrimSpace(arg) == "" {
			m.appendSys("Usage: /remove-dir <path>")
			break
		}
		_ = m.sess.RemoveWorkingDir(arg)
		m.appendSys("Removing working directory…")
	case "theme":
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" {
			trimmed = nextThemeName(m.theme)
		} else if strings.EqualFold(trimmed, "next") {
			trimmed = nextThemeName(m.theme)
		}
		canon, ok := parseThemeKind(trimmed)
		if !ok {
			m.appendSys(unknownThemeError(trimmed))
			break
		}
		m.theme = canon
		applyTheme(resolveTheme(canon))
		saveTheme(canon)
		m.appendSys(saveThemeToast(canon))
	case "feedback":
		if arg != "" {
			m.sendFeedback(arg)
			break
		}
		m.overlay = overlayFeedback
		m.feedback = ""
	case "announcements":
		tok := ""
		if f := strings.Fields(arg); len(f) > 0 {
			tok = strings.ToLower(f[0])
		}
		switch tok {
		case "hide":
			m.setAnnouncementsHidden(true)
		case "show":
			m.setAnnouncementsHidden(false)
		default:
			m.appendSys("Usage: /announcements hide | show")
		}
	case "remember":
		return m.remember(arg)
	case "plan":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		m.armPlanMode()
		m.appendSys(saveSuccessToast("Plan mode", true))
		if arg != "" {
			_ = os.WriteFile(m.sess.Plan.PlanFile, []byte("# Plan\n\n"+arg+"\n"), 0o600)
			return m.submit("Enter plan mode. Draft the plan in the session plan file. Description: " + arg)
		}
	case "swarm":
		return m.handleSwarm(arg)
	case "view-plan":
		if m.sess == nil {
			m.appendSys("No active session")
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
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		if strings.TrimSpace(arg) == "" {
			m.appendSys("Usage: /btw <question>")
			break
		}
		return m.startBTW(arg)
	case "recap":
		return m.sendRecap()
	case "doctor":
		return m.handleDoctor(arg)
	case "voice":
		return m.toggleVoice()
	case "loop":
		if arg == "" {
			m.appendSys(loopUsageMessage())
			break
		}
		return m.submitDisplay("/loop "+arg, loopScheduleInstruction(arg, m.loopFireMode()))
	case "imagine":
		if arg == "" {
			m.appendSys(imagineUsageMessage())
			break
		}
		return m.submitDisplay("/imagine "+arg, imagineInstruction(arg))
	case "imagine-video":
		if arg == "" {
			m.appendSys(imagineVideoUsageMessage())
			break
		}
		return m.submitDisplay("/imagine-video "+arg, imagineVideoInstruction(arg))
	case "timestamps":
		m.timestamps = !m.timestamps
		m.persistUI()
		m.appendSys(saveSuccessToast("Timestamps", m.timestamps))
	case "timeline":
		m.cfg.UI.ShowTimeline = !m.cfg.UI.ShowTimeline
		m.persistUI()
		m.layoutChat()
		m.appendSys("\u2713 Timeline sidebar: " + onOff(m.cfg.UI.ShowTimeline))
	case "toggle-mouse-reporting":
		m.mouseOn = !m.mouseOn
		m.persistUI()
		m.appendSys("mouse reporting = " + onOff(m.mouseOn) + " (takes effect next launch)")
	case "settings":
		m.openSettings()
	case "privacy":
		m.openSettingsFocus("coding_data_sharing")
	case "rewind":
		m.openRewindPicker()
	case "jump":
		m.openJump()
	case "login":
		if arg != "" {
			if _, ok := parseLoginProvider(arg); !ok {
				m.appendSys(unknownLoginProvider(arg))
				break
			}
		}
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
		m.openCache()
	case "queue":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		if strings.EqualFold(arg, "clear") {
			m.queue = nil
			m.appendSys("queue cleared")
			m.overlay = overlayNone
			break
		}
		m.appendSys(m.queueBlockText())
	case "tasks":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		m.appendSys(m.tasksBlockText())
	case "release-notes":
		m.openReleaseNotes()
	case "tutorial":
		m.openTutorial()
	case "config-agents":
		m.openAgents(agentsTabAgents)
	case "personas":
		m.openAgents(agentsTabPersonas)
	case "gboom":
		if m.sess == nil {
			m.appendSys("No active session")
			break
		}
		if strings.TrimSpace(arg) != "" {
			return m.submit("/gboom " + arg)
		}
		m.appendSys("💥 gboom")
	case "scroll-debug":
		m.scrollHUD = !m.scrollHUD
	case "debug":
		m.handleDebug(arg)
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
		return m.startWorkflow("deep-research", "Deep research", arg,
			"Deep-research workflow: plan bounded questions, gather sourced claims, cross-check, and report only surviving claims with locators. Query: "+arg)
	case "ultracode":
		if arg == "" {
			m.appendSys("usage: /ultracode <task>")
			break
		}
		return m.startWorkflow("ultracode", "Ultracode", arg,
			"Ultracode workflow: scout, competing plans, implement, adversarial review. Task: "+arg)
	case "workflow":
		return m.handleWorkflow(arg)
	default:
		m.appendSys("unhandled /" + name)
	}
	m.refreshVP()
	return nil
}

func saveSuccessToast(label string, on bool) string {
	return "\u2713 " + label + ": " + onOff(on)
}

func saveThemeToast(canonical string) string {
	return "\u2713 Theme: " + themePrettyName(canonical)
}

func yoloToast(on, planActive bool) string {
	if !on {
		return saveSuccessToast("Always-approve", false)
	}
	if planActive {
		return "\u26A0 Always-approve ON: plan mode still blocks file edits until you exit plan mode"
	}
	return "\u26A0 Always-approve ON: all tool actions auto-run"
}

func permissionModeToast(kind permMode) string {
	switch kind {
	case permAuto:
		return "\u2713 Permission mode: Auto (classifier)"
	case permAlways:
		return yoloToast(true, false)
	default:
		return "\u2713 Permission mode: Ask"
	}
}

const yolo2SandboxError = "YOLO-2 requires an active OS sandbox. Restart Open Grok with `--sandbox workspace` before enabling it."

const feedbackQuestionLabel = "How can we improve Grok Build?"

const feedbackThanks = "Thanks for the feedback! The Open Grok team is on it."

func (m *model) toggleAlwaysApprove() {
	if m.permMode == permAlways {
		m.permMode = permAsk
		if m.sess != nil {
			m.sess.YOLO = false
		}
		m.appendSys(yoloToast(false, m.sess != nil && m.sess.Plan.Active))
		return
	}
	m.permMode = permAlways
	if m.sess != nil {
		m.sess.YOLO = true
	}
	m.appendSys(yoloToast(true, m.sess != nil && m.sess.Plan.Active))
}

func (m *model) sendFeedback(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		m.appendSys("Please provide feedback text.")
		m.refreshVP()
		return
	}
	m.appendSys(feedbackThanks)
	m.refreshVP()
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
	win := m.opts.Model.ContextWindow
	if win == 0 {
		win = 128_000
	}
	used := bytes / 4
	pct := usagePct(used, win)
	var b strings.Builder
	if t := strings.TrimSpace(m.sess.Title); t != "" {
		fmt.Fprintf(&b, "  Title: %s\n", t)
	}
	fmt.Fprintf(&b, "  Shell version: %s\n", version.String())
	b.WriteString(sessionAuthLines(m.opts.Model.Provider))
	fmt.Fprintf(&b, "  Session ID: %s\n", m.sess.ID)
	cwd := m.sess.CWD
	if cwd == "" {
		cwd = m.opts.CWD
	}
	fmt.Fprintf(&b, "  Working directory: %s\n", cwd)
	modelName := m.opts.Model.Name
	if modelName == "" {
		modelName = m.opts.Model.ID
	}
	fmt.Fprintf(&b, "  Model: %s\n", modelName)
	backend := string(m.opts.Model.APIBackend)
	if backend == "" {
		backend = string(m.sess.Backend)
	}
	if backend != "" {
		fmt.Fprintf(&b, "  API Backend: %s\n", backend)
	}
	if sandboxActive() {
		net := ""
		if config.FirstEnv("ENTROPY_SANDBOX_NET", "OPENGROK_SANDBOX_NET", "GROK_SANDBOX_RESTRICT_NETWORK") != "" {
			net = " (network: restricted)"
		}
		fmt.Fprintf(&b, "  Sandbox: entropy%s\n", net)
	}
	fmt.Fprintf(&b, "  Turn: %d\n", turns)
	fmt.Fprintf(&b, "  Context: %d / %d tokens (%.0f%%)", used, win, pct)
	return b.String()
}

func sessionAuthLines(p types.Provider) string {
	envSet := false
	used := ""
	for _, k := range auth.EnvKeys(p) {
		if os.Getenv(k) != "" {
			envSet = true
			used = k
			break
		}
	}
	st := auth.Status()
	_, signed := st[p]
	if envSet {
		method := "  Auth method: API key\n"
		if used != "" {
			method = fmt.Sprintf("  Auth method: API key (%s)\n", used)
		}
		hint := "  Run `entropy login` to use a stored credential instead.\n"
		if p == types.ProviderXAI {
			hint = "  Run `entropy login` to use your SuperGrok subscription instead.\n"
		}
		return method + hint
	}
	if signed && p == types.ProviderCodex {
		return "  Auth method: OAuth\n"
	}
	if signed {
		return "  Auth method: API key\n"
	}
	return "  Auth method: signed out\n"
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
		m.enterRememberMode()
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
		m.openMemory(strings.TrimSpace(arg))
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
		m.appendSys("Swarm task started; manual approvals may slow the swarm.")
		return m.submit("Swarm mode: partition this task across parallel subagents (depth 1), then merge. Task: " + arg)
	}
	if m.sess != nil {
		m.sess.Swarm = m.swarm
	}
	m.persistUI()
	m.appendSys(saveSuccessToast("Swarm mode", m.swarm))
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

func (m *model) loopFireMode() loopFireMode {
	if m.cfg.Prefs != nil {
		v := strings.ToLower(strings.TrimSpace(m.cfg.Prefs["scheduler.background_loops"]))
		if v == "false" || v == "off" || v == "0" {
			return loopFireInSession
		}
	}
	return loopFireDetached
}

func (m *model) reloadLoops() tea.Cmd {
	if m.sess == nil {
		m.loops = nil
		return nil
	}
	tasks := tools.LoadScheduledTasks(m.sess.Dir)
	now := time.Now()
	seen := map[string]bool{}
	var next []loopJob
	var cmds []tea.Cmd
	for _, t := range tasks {
		seen[t.ID] = true
		job := loopJob{
			ID:     t.ID,
			Prompt: t.Prompt,
			Every:  time.Duration(t.IntervalSecs) * time.Second,
			Next:   t.NextFireAt,
		}
		if t.ExpiresAt != nil {
			job.Until = *t.ExpiresAt
		}
		for _, old := range m.loops {
			if old.ID == t.ID {
				job.ticking = old.ticking
				job.stopped = old.stopped
			}
		}
		if !job.stopped && (job.Until.IsZero() || now.Before(job.Until)) && !job.ticking {
			job.ticking = true
			delay := time.Until(job.Next)
			if delay < 0 {
				delay = 0
			}
			id := job.ID
			cmds = append(cmds, tea.Tick(delay, func(time.Time) tea.Msg { return loopTickMsg{id: id} }))
		}
		next = append(next, job)
	}
	for _, old := range m.loops {
		if !seen[old.ID] {
			old.stopped = true
		}
	}
	m.loops = next
	return tea.Batch(cmds...)
}

func (m *model) onLoopTick(id string) tea.Cmd {
	var job *loopJob
	for i := range m.loops {
		if m.loops[i].ID == id {
			job = &m.loops[i]
			break
		}
	}
	if job == nil || job.stopped || (!job.Until.IsZero() && time.Now().After(job.Until)) {
		if job != nil {
			job.ticking = false
		}
		return nil
	}
	if m.sess != nil {
		_ = tools.MarkScheduledFire(m.sess.Dir, id, time.Now())
	}
	job.ticking = false
	prompt := job.Prompt
	arm := m.reloadLoops()
	var fire tea.Cmd
	if m.busy {
		m.queue = append(m.queue, prompt)
	} else {
		fire = m.submit(prompt)
	}
	return tea.Batch(fire, arm)
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
