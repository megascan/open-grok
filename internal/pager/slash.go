package pager

import (
	"strconv"
	"strings"
	"unicode"
)

// slashCmd is one pager or shell-advertised command. Names and aliases match
// the Rust xai-grok-pager builtin_commands() registry plus shell-advertised
// commands from the user guide (goal, memory, flush, dream, workflows).
type slashCmd struct {
	Name     string
	Aliases  []string
	Help     string
	Hidden   bool
	Mode     string // "", "fullscreen", "minimal"
	Category string
}

const (
	modeFull    = "fullscreen"
	modeMinimal = "minimal"
)

var slashCatalog = []slashCmd{
	{Name: "quit", Aliases: []string{"exit"}, Help: "quit the application", Category: "session"},
	{Name: "help", Help: "list commands", Category: "session"},
	{Name: "docs", Aliases: []string{"howto", "guides"}, Help: "browse in-TUI how-to guides", Category: "help"},
	{Name: "home", Aliases: []string{"welcome"}, Help: "return to the welcome screen", Category: "session"},
	{Name: "delete", Help: "delete this session after confirm", Category: "session"},
	{Name: "new", Aliases: []string{"clear"}, Help: "start a fresh session", Category: "session"},
	{Name: "fork", Help: "branch this conversation into a new session", Category: "session"},
	{Name: "compact", Help: "compress history to reclaim context", Category: "session"},
	{Name: "copy", Help: "copy the latest reply (or write a file)", Category: "session"},
	{Name: "find", Help: "search the scrollback", Mode: modeFull, Category: "view"},
	{Name: "history", Help: "fuzzy-search this session's prompts", Category: "session"},
	{Name: "export", Help: "export the conversation to markdown", Category: "session"},
	{Name: "transcript", Aliases: []string{"log"}, Help: "show the raw transcript", Category: "session"},
	{Name: "edit-prompt", Help: "edit the draft in $VISUAL/$EDITOR", Mode: modeMinimal, Category: "session"},
	{Name: "expand", Help: "expand the selected scrollback block", Mode: modeMinimal, Category: "view"},
	{Name: "context", Help: "show context-window usage", Category: "session"},
	{Name: "minimal", Help: "switch this session to minimal mode", Category: "view"},
	{Name: "fullscreen", Aliases: []string{"full"}, Help: "switch this session to fullscreen mode", Category: "view"},
	{Name: "model", Aliases: []string{"m"}, Help: "switch or show the current model", Category: "model"},
	{Name: "effort", Help: "set reasoning effort (low|medium|high|xhigh)", Category: "model"},
	{Name: "fast", Help: "toggle Codex Fast service tier", Category: "model"},
	{Name: "always-approve", Aliases: []string{"yolo"}, Help: "skip permission prompts", Category: "permissions"},
	{Name: "yolo-2", Help: "always-approve only inside an OS sandbox", Category: "permissions"},
	{Name: "auto", Help: "auto-approve safe tools; bash still asks", Category: "permissions"},
	{Name: "multiline", Aliases: []string{"ml"}, Help: "Enter inserts a newline; Alt+Enter sends", Category: "input"},
	{Name: "compact-mode", Help: "tighter scrollback spacing", Category: "view"},
	{Name: "vim-mode", Help: "vim-style scrollback keys", Category: "input"},
	{Name: "hooks", Help: "open the extensions modal on Hooks", Category: "extensions"},
	{Name: "plugins", Help: "open the extensions modal on Plugins", Category: "extensions"},
	{Name: "marketplace", Help: "open the extensions modal on Marketplace", Category: "extensions"},
	{Name: "skills", Help: "open the extensions modal on Skills", Category: "extensions"},
	{Name: "share", Help: "Share this session via URL", Category: "session"},
	{Name: "session-info", Aliases: []string{"status", "info"}, Help: "Show session info", Category: "session"},
	{Name: "rename", Aliases: []string{"title"}, Help: "rename this session", Category: "session"},
	{Name: "dashboard", Aliases: []string{"agents-dashboard", "sessions"}, Help: "live roster of sessions", Mode: modeFull, Category: "session"},
	{Name: "cd", Help: "directory used for new dashboard sessions", Category: "session"},
	{Name: "add-dir", Help: "add a working-set directory to this session", Category: "session"},
	{Name: "remove-dir", Help: "remove a working-set directory", Category: "session"},
	{Name: "theme", Aliases: []string{"t"}, Help: "switch the TUI color theme", Mode: modeFull, Category: "view"},
	{Name: "feedback", Help: "report an issue or send feedback", Category: "help"},
	{Name: "announcements", Help: "show product announcements", Category: "help"},
	{Name: "remember", Help: "save a note to memory immediately", Category: "memory"},
	{Name: "plan", Help: "enter plan mode (hard edit gate)", Category: "permissions"},
	{Name: "swarm", Help: "toggle swarm mode or run a one-shot swarm", Category: "agents"},
	{Name: "view-plan", Aliases: []string{"show-plan", "plan-view"}, Help: "preview the saved plan.md", Category: "permissions"},
	{Name: "resume", Help: "open the session picker", Category: "session"},
	{Name: "mcps", Aliases: []string{"mcp"}, Help: "MCP servers management", Category: "extensions"},
	{Name: "workflows", Help: "live workflow run dashboard", Mode: modeFull, Category: "workflows"},
	{Name: "btw", Help: "aside to the agent without interrupting the turn", Category: "session"},
	{Name: "recap", Aliases: []string{"summarize"}, Help: "summarize the conversation so far", Category: "session"},
	{Name: "doctor", Aliases: []string{"terminal-setup", "terminal-check", "terminal-info"}, Help: "Check this session and show available fixes", Category: "help"},
	{Name: "voice", Help: "toggle dictation (Ctrl+Space/F8; Esc/Enter to stop)", Category: "input"},
	{Name: "loop", Help: "run a prompt on a recurring interval", Category: "scheduling"},
	{Name: "imagine", Help: "generate an image from a description", Category: "media"},
	{Name: "imagine-video", Help: "generate a video from a description", Category: "media"},
	{Name: "timestamps", Help: "toggle message timestamps", Category: "view"},
	{Name: "timeline", Help: "jump the scrollback by turn", Mode: modeFull, Category: "view"},
	{Name: "toggle-mouse-reporting", Help: "toggle mouse cell-motion reporting", Category: "input"},
	{Name: "settings", Aliases: []string{"config", "preferences", "prefs"}, Help: "open the settings modal", Category: "config"},
	{Name: "privacy", Help: "coding data, retention, and training", Category: "config"},
	{Name: "rewind", Aliases: []string{"undo"}, Help: "roll back the last exchange", Category: "session"},
	{Name: "jump", Help: "jump to a turn in the scrollback", Mode: modeFull, Category: "view"},
	{Name: "login", Help: "Connect xAI, OpenAI Codex, Kimi, Fireworks AI, DeepSeek, Meta API, Wafer AI, Z AI, RunInfra, Google Gemini, OpenCode Go, or OpenRouter", Category: "account"},
	{Name: "logout", Help: "sign out and return to login", Category: "account"},
	{Name: "import-claude", Help: "import ~/.claude settings into ~/.entropy", Category: "account"},
	{Name: "usage", Aliases: []string{"cost"}, Help: "provider quota / billing summary", Category: "account"},
	{Name: "cache", Aliases: []string{"cache-status", "prompt-cache"}, Help: "Compare all-provider and supported cache hit rates with reset diagnostics", Category: "session"},
	{Name: "queue", Help: "show or clear mid-turn follow-ups", Category: "session"},
	{Name: "tasks", Help: "List background tasks, subagents, and scheduled tasks", Category: "session"},
	{Name: "release-notes", Aliases: []string{"changelog"}, Help: "notes for this Entropy version", Category: "help"},
	{Name: "tutorial", Aliases: []string{"tour", "onboarding"}, Help: "onboarding topics", Mode: modeFull, Category: "help"},
	{Name: "config-agents", Aliases: []string{"agents"}, Help: "agent definitions and personas", Category: "agents"},
	{Name: "personas", Help: "create, edit, and delete personas", Category: "agents"},
	{Name: "gboom", Help: "easter egg", Hidden: true, Category: "hidden"},
	{Name: "scroll-debug", Help: "toggle the scroll-debug HUD", Hidden: true, Category: "hidden"},
	{Name: "debug", Help: "debug toggles", Hidden: true, Category: "hidden"},
	// Shell-advertised (same menu in the Rust pager).
	{Name: "goal", Help: "<objective> | status | pause | resume | complete | clear", Category: "workflows"},
	{Name: "memory", Aliases: []string{"mem"}, Help: "browse or toggle persisted memory", Category: "memory"},
	{Name: "flush", Help: "save this session's knowledge to memory now", Category: "memory"},
	{Name: "dream", Help: "consolidate memory notes into topics", Category: "memory"},
	{Name: "deep-research", Help: "background research workflow", Category: "workflows"},
	{Name: "ultracode", Help: "maximum-rigor coding workflow", Category: "workflows"},
	{Name: "workflow", Help: "launch or manage a saved workflow", Category: "workflows"},
}

var slashIndex map[string]*slashCmd

func init() {
	slashIndex = make(map[string]*slashCmd, len(slashCatalog)*3)
	for i := range slashCatalog {
		c := &slashCatalog[i]
		slashIndex[c.Name] = c
		for _, a := range c.Aliases {
			slashIndex[a] = c
		}
	}
}

// ResolveSlash maps a typed name or alias to the canonical command.
func ResolveSlash(name string) (slashCmd, bool) {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	if name == "" {
		return slashCmd{}, false
	}
	c, ok := slashIndex[name]
	if !ok {
		return slashCmd{}, false
	}
	return *c, true
}

func visibleCatalog(screenMode string, includeHidden bool) []slashCmd {
	out := make([]slashCmd, 0, len(slashCatalog))
	for _, c := range slashCatalog {
		if c.Hidden && !includeHidden {
			continue
		}
		if c.Mode != "" && c.Mode != screenMode {
			continue
		}
		out = append(out, c)
	}
	return out
}

func matchingSlash(prefix string) []slashCmd {
	return FuzzySlash(prefix, modeFull)
}

// FuzzySlash ranks commands by prefix, then subsequence, then help text.
func FuzzySlash(prefix, screenMode string) []slashCmd {
	p := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(prefix), "/"))
	pool := visibleCatalog(screenMode, false)
	if p == "" {
		return pool
	}
	type scored struct {
		c slashCmd
		n int
	}
	var hits []scored
	for _, c := range pool {
		best := scoreName(p, c.Name)
		for _, a := range c.Aliases {
			if s := scoreName(p, a); s > best {
				best = s
			}
		}
		if best == 0 && strings.Contains(strings.ToLower(c.Help), p) {
			best = 1
		}
		if best > 0 {
			hits = append(hits, scored{c, best})
		}
	}
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].n > hits[i].n || (hits[j].n == hits[i].n && hits[j].c.Name < hits[i].c.Name) {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	out := make([]slashCmd, len(hits))
	for i, h := range hits {
		out[i] = h.c
	}
	return out
}

func scoreName(q, name string) int {
	name = strings.ToLower(name)
	if name == q {
		return 1000
	}
	if strings.HasPrefix(name, q) {
		return 500 - len(name)
	}
	if strings.Contains(name, q) {
		return 200
	}
	// subsequence (fuzzy)
	qi := 0
	run := 0
	bestRun := 0
	for _, r := range name {
		if qi < len(q) && unicode.ToLower(r) == rune(q[qi]) {
			qi++
			run++
			if run > bestRun {
				bestRun = run
			}
		} else {
			run = 0
		}
	}
	if qi == len(q) {
		return 50 + bestRun*10
	}
	return 0
}

type goalVerb int

const (
	goalVerbStatus goalVerb = iota
	goalVerbPause
	goalVerbResume
	goalVerbComplete
	goalVerbClear
	goalVerbStart
)

type goalSlash struct {
	Verb      goalVerb
	Objective string
	Budget    *int64
}

func parseGoalSlash(arg string) goalSlash {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return goalSlash{Verb: goalVerbStatus}
	}
	switch strings.ToLower(fields[0]) {
	case "status":
		return goalSlash{Verb: goalVerbStatus}
	case "pause":
		return goalSlash{Verb: goalVerbPause}
	case "resume":
		return goalSlash{Verb: goalVerbResume}
	case "complete", "accept":
		return goalSlash{Verb: goalVerbComplete}
	case "clear":
		return goalSlash{Verb: goalVerbClear}
	}
	var budget *int64
	var obj []string
	for i := 0; i < len(fields); i++ {
		if fields[i] == "--budget" && i+1 < len(fields) {
			if n, err := strconv.ParseInt(fields[i+1], 10, 64); err == nil {
				budget = &n
			}
			i++
			continue
		}
		obj = append(obj, fields[i])
	}
	return goalSlash{Verb: goalVerbStart, Objective: strings.Join(obj, " "), Budget: budget}
}

// CanonicalSlashNames is the builtin + shell-advertised set (tests).
func CanonicalSlashNames() []string {
	out := make([]string, 0, len(slashCatalog))
	for _, c := range slashCatalog {
		out = append(out, c.Name)
	}
	return out
}
