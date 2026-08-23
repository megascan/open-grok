package pager

import "strings"

// Canonical /loop wording from the Rust xai-grok-tools-api slash_commands
// crate so the model instruction cannot drift from the reference pager.

func loopUsageMessage() string {
	return "Usage: /loop [interval] <prompt>\n" +
		"Example: /loop 30m check deploy status\n" +
		"Example: /loop check deploy status every hour\n\n" +
		"Tell me how often it should run (e.g. 30m, 1 hour, every 2 days)."
}

type loopFireMode int

const (
	loopFireDetached loopFireMode = iota
	loopFireInSession
)

func loopScheduleInstruction(args string, mode loopFireMode) string {
	fireContext := "" +
		"Each fire runs in a detached background subagent, not in this conversation,\n" +
		"so the prompt you store must stand on its own.\n\n" +
		"## Writing a prompt that survives a fresh fire\n" +
		"- Inline the state a fire needs: paths, job/PR/branch ids, the command that checks\n" +
		"  status, and what \"healthy\" looks like. A fire cannot see this conversation, and\n" +
		"  a long-running task restarts from a short summary every few iterations.\n" +
		"- Only a short status comes back here, so say what that status must contain."
	if mode == loopFireInSession {
		fireContext = "" +
			"Each fire arrives as a new turn in this conversation, and earlier results from\n" +
			"the same task may still be above it. The stored prompt is re-sent verbatim every\n" +
			"time, so write a standing order rather than a one-off request.\n\n" +
			"## Writing a prompt that reads well on every fire\n" +
			"- Name the state that must not be guessed: paths, job/PR/branch ids, the command\n" +
			"  that checks status, and what \"healthy\" looks like. This conversation is\n" +
			"  compacted as it grows, so do not rely on details staying visible.\n" +
			"- Earlier fires may be above you: continue from them instead of restarting."
	}
	return "# /loop -- schedule a recurring prompt\n\n" +
		"Turn the input below into a scheduler_create call. " + fireContext + "\n" +
		"- Say what one fire does and when it bails: \"if still pending, report one line and\n" +
		"  stop.\" A fire must not poll inline.\n" +
		"- Give it a stop condition and an exit: \"when <condition> holds, report it and call\n" +
		"  scheduler_delete <task_id>.\" Without that the loop runs until it expires.\n" +
		"- Keep it short and concrete -- the stored prompt is re-sent on every fire.\n\n" +
		"## Deriving the interval\n" +
		"Convert the user's cadence -- however phrased, at either end of the request -- into a\n" +
		"compact `<number><unit>` string (`s`/`m`/`h`/`d`); the remaining text is the prompt.\n" +
		"The minimum is 60 seconds and shorter values are raised, so say so when it applies.\n" +
		"If no cadence is given, ask the user how often it should run -- never invent one.\n\n" +
		"## Action\n" +
		"Schedule from what the user already gave you — do not explore the workspace or run\n" +
		"checks before scheduling; the first fire does that.\n" +
		"1. Call scheduler_create with the interval, the prompt, and fire_immediately: true.\n" +
		"   If the interval is rejected, fix the string rather than guessing.\n" +
		"2. Confirm what's scheduled, the cadence, its stop condition, that it auto-expires\n" +
		"   after 7 days, and the task_id to cancel with scheduler_delete.\n" +
		"3. Do NOT execute the prompt inline. The scheduler fires it immediately.\n\n" +
		"## Wrong tool for the job\n" +
		"- \"Tell me when X finishes\" -> a background command or watch tool that wakes you on\n" +
		"  the event, not a recurring loop that re-checks on a timer.\n" +
		"- \"Do X once in N minutes\" -> background `sleep <secs> && <command>`; scheduling is\n" +
		"  recurring-only.\n\n" +
		"## Changing an existing loop\n" +
		"Call scheduler_create with its task_id and only the changed fields; do not\n" +
		"delete and recreate. If later work changes what a loop should do, update its\n" +
		"prompt the same way.\n\n" +
		"## Input\n" +
		args
}

func unknownLoginProvider(arg string) string {
	return "Unknown provider: " + strings.TrimSpace(arg) +
		". Use /login xai, /login codex, /login kimi, /login fireworks, /login deepseek, /login meta, /login wafer, /login zai, /login runinfra, /login gemini, /login opencode-go, or /login openrouter"
}
