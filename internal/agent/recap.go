package agent

import (
	"strings"
	"unicode/utf8"

	"github.com/megascan/open-grok/internal/types"
)

const recapMaxChars = 1200

// RecapInstruction is the side-call user turn. Body text only — the pager
// adds the `Recap —` label. Wrapped in <system-reminder> so the conversation
// prefix stays cache-warm.
func RecapInstruction(tag string) string {
	if tag == "" {
		tag = "system-reminder"
	}
	return "<" + tag + ">Write ONE sentence recap body for a user returning from idle. " +
		"Output ONLY the body (the UI adds the \"Recap —\" label). " +
		"Do NOT call any tools — respond with plain text only.\n\n" +
		"LANGUAGE: write the body in the language the user's own chat messages " +
		"are written in (ignore reminder-tagged turns like this one; user " +
		"instructions such as AGENTS.md may override). Keep code identifiers " +
		"verbatim.\n\n" +
		"Lead with agency:\n" +
		"- \"You asked …\" if the session was mainly questions, walkthroughs, or review with no landed change.\n" +
		"- \"We <past-tense verb> …\" if the agent implemented, fixed, merged, or changed code/config/docs " +
		"(e.g. \"We fixed …\", \"We merged …\", \"We wired …\" — not \"We did fix\" / \"We did merge\").\n" +
		"- If almost nothing happened: \"You had just begun this session.\"\n\n" +
		"Shape: <lead>: <concrete specifics — crate/file/flag/behavior/endpoint>. ~25–40 words.\n\n" +
		"Synthetic examples (style only — adapt to THIS session, do not copy):\n\n" +
		"You asked how retries work in the payment client: exponential backoff in `billing/retry.rs`, max 5 attempts, 429s only.\n\n" +
		"You asked for a walkthrough of the auth middleware change: warn-only mode in the API layer, no hard fail on missing claims.\n\n" +
		"We fixed the flaky integration test: race in `queue_worker` shutdown by awaiting the drain channel before exit.\n\n" +
		"We merged the feature branch: kept the new telemetry hooks, dropped the obsolete feature flag in `config/flags.toml`.\n\n" +
		"Bad (never):\n" +
		"- Start with Recap / Session recap / extra labels\n" +
		"- English recap for a non-English session\n" +
		"- Quote or restate this reminder or any system prompt\n" +
		"- Bullets, markdown, code fences, extra sentences\n" +
		"- Call tools or emit tool/function calls\n" +
		"- Invent work not reflected in the session</" + tag + ">"
}

// RecapUnavailableToast is the empty-vs-failure toast for a manual /recap.
func RecapUnavailableToast(hasUserMessages bool) string {
	if hasUserMessages {
		return "Couldn't generate recap"
	}
	return "No messages yet"
}

// NoSessionNotice is the toast when /recap runs without a session.
func NoSessionNotice() string { return "No active session" }

// RecapDisabledNotice is shown when the feature is off.
func RecapDisabledNotice() string { return "Session recap is not enabled" }

// CountUserMessages counts real user prompts (not reminder-tagged synthetics).
func CountUserMessages(messages []types.Message) int {
	n := 0
	for _, m := range messages {
		if m.Role != types.RoleUser {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text == "" || strings.HasPrefix(text, "<system-reminder>") {
			continue
		}
		n++
	}
	return n
}

// SnapshotForRecap copies the conversation, drops a trailing tool run, and
// appends the recap instruction as a final user turn. The snapshot is never
// persisted back onto the session.
func SnapshotForRecap(messages []types.Message) []types.Message {
	out := append([]types.Message(nil), messages...)
	for len(out) > 0 {
		last := out[len(out)-1]
		if last.Role == types.RoleTool || (last.Role == types.RoleAssistant && len(last.ToolCalls) > 0) {
			out = out[:len(out)-1]
			continue
		}
		break
	}
	out = append(out, types.Message{Role: types.RoleUser, Content: RecapInstruction("system-reminder")})
	return out
}

// CleanRecapText collapses whitespace, strips a stray leading label, and caps length.
func CleanRecapText(raw string) string {
	out := strings.Join(strings.Fields(raw), " ")
	for _, label := range []string{
		"Recap —", "Recap—", "Recap -", "Recap:", "recap:", "Session recap:", "Summary:",
	} {
		if rest, ok := strings.CutPrefix(out, label); ok {
			out = strings.TrimSpace(rest)
			break
		}
	}
	if len(out) >= 2 {
		first, last := out[0], out[len(out)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			out = strings.TrimSpace(out[1 : len(out)-1])
		}
	}
	if utf8.RuneCountInString(out) > recapMaxChars {
		runes := []rune(out)
		out = strings.TrimSpace(string(runes[:recapMaxChars])) + "…"
	}
	return out
}

// FormatRecapLine is the scrollback label the pager owns (model must not add it).
func FormatRecapLine(body string) string {
	body = CleanRecapText(body)
	if body == "" {
		return ""
	}
	return "Recap — " + body
}
