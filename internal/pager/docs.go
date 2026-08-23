package pager

import (
	"os"
	"path/filepath"
	"strings"
)

func docsItems() []listItem {
	titles := []struct{ title, body string }{
		{"Getting Started", "Install Entropy, run `entropy`, sign in with `/login`, and send a first prompt. State lives in $ENTROPY_HOME or ~/.entropy — never ~/.grok."},
		{"Authentication", "Each provider has an isolated credential store. `/login` / `/login codex` / `/login openrouter`. An explicit model API key wins over OAuth. API backend is not credentials."},
		{"Keyboard Shortcuts", "Tab focuses prompt vs scrollback. Shift+Tab cycles Normal / Plan / Always-approve. Ctrl+\\ opens the dashboard. Esc cancels, clears, or quits. Type / for the fuzzy command palette."},
		{"Slash Commands", "Builtins and user-invocable skills share one menu. Builtins win bare names; colliding skills stay as /local:name. Aliases resolve to the same command."},
		{"Configuration", "~/.entropy/config.toml [ui] theme, vim_mode, timestamps, compact_mode, multiline, screen_mode, swarm_mode. CLI flags beat env beat config."},
		{"Theming", "Themes: groknight, grokday, tokyonight, rosepine, oscura, auto. /theme opens a live preview picker. Minimal mode uses the terminal palette."},
		{"MCP Servers", "Configure [mcp_servers] in config.toml. /mcps opens the MCP tab of the extensions modal."},
		{"Skills", "SKILL.md files under .entropy/skills, .opengrok/skills, and ~/.entropy/skills. user-invocable: true advertises a slash command."},
		{"Plugins", "/plugins and /marketplace open the extensions modal. Project trust is separate from hook execution."},
		{"Hooks", "PreToolUse/PostToolUse scripts fail open. Exit 2 denies a tool. Combine with deny rules and the sandbox."},
		{"Project Rules", "AGENTS.md / Claude.md discovery for the workspace. Entropy never writes ~/.grok."},
		{"Memory", "/remember always works. /memory /flush /dream need ENTROPY_MEMORY=1 or [features] memory."},
		{"Sessions", "Each session is ~/.entropy/sessions/<cwd>/<id> with session.json, summary.json, updates.jsonl, plan.md, goal/."},
		{"Subagents", "Max depth 1. Children inherit YOLO and allow/deny, and get a fresh Inactive plan tracker."},
		{"Plan Mode", "Only session_dir/plan.md may be edited. The gate runs before hooks/permissions. Shift+Tab cycles onto Plan."},
		{"Permissions", "Order: plan gate → hooks → plan-file auto-approve → permission manager. Ask opens this TUI card (y/a/n)."},
		{"Dashboard", "/dashboard or Ctrl+\\ lists sessions. Hidden in minimal mode or when ENTROPY_AGENT_DASHBOARD=0."},
		{"Code Mode", "Model metadata selects Direct / Code Mode / Code Mode Only. exec/wait are transport, not tool cards."},
	}
	out := make([]listItem, 0, len(titles)+1)
	out = append(out, listItem{Key: "https://docs.x.ai/build/overview", Label: "docs web", Detail: "open the online Build docs URL"})
	if root := findUserGuide(); root != "" {
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, e.Name()))
			if err != nil {
				continue
			}
			title := strings.TrimSuffix(e.Name(), ".md")
			out = append(out, listItem{Key: string(data), Label: title, Detail: truncate(string(data), 80)})
		}
		if len(out) > 1 {
			return out
		}
	}
	for _, t := range titles {
		out = append(out, listItem{Key: t.body, Label: t.title, Detail: truncate(t.body, 80)})
	}
	return out
}

func tutorialItems() []listItem {
	topics := []struct{ title, body string }{
		{"Your first prompt", "From the welcome screen pick New session (or press enter on it). Type a question and press Enter. Entropy streams the reply into the scrollback. /help lists every command."},
		{"Attaching context", "The session CWD is the workspace. /add-dir widens read/edit to another tree without restarting. @-file search is the palette's sibling: tools read the files you name."},
		{"Navigation", "Tab focuses the scrollback. Arrows (or j/k in /vim-mode) move. PageUp/PageDown scroll from the prompt. y copies a block; backup lands in ~/.entropy/last-copy.txt."},
		{"Slash commands", "Type / to open the fuzzy palette. Tab completes, Enter runs. Builtins win collisions with skills. /settings, /theme, /login, /dashboard, /goal, /plan are the daily drivers."},
		{"Worktrees", "Subagents can isolate to a worktree. Depth is capped at 1. They inherit always-approve and get a fresh Inactive plan tracker."},
		{"Plan mode", "Shift+Tab or /plan. Only session_dir/plan.md is writable. Other edits are rejected before hooks. Exit with Shift+Tab or /plan again."},
		{"Customization", "/theme, /vim-mode, /timestamps, /compact-mode, /multiline, /minimal, /fullscreen. All persist to [ui] in ~/.entropy/config.toml."},
		{"Switching from another tool", "/import-claude copies ~/.claude MCP, hooks, and permissions into ~/.entropy. Credentials stay provider-isolated."},
	}
	out := make([]listItem, 0, len(topics))
	for _, t := range topics {
		out = append(out, listItem{Key: t.body, Label: t.title, Detail: "→ next topic"})
	}
	return out
}

func findUserGuide() string {
	candidates := []string{
		"crates/codegen/xai-grok-pager/docs/user-guide",
		filepath.Join("..", "crates/codegen/xai-grok-pager/docs/user-guide"),
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append([]string{filepath.Join(wd, "crates/codegen/xai-grok-pager/docs/user-guide")}, candidates...)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return ""
}
