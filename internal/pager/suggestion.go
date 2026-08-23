package pager

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/sampler"
	"github.com/megascan/open-grok/internal/types"
)

const (
	suggestMaxChars  = 120
	suggestMaxWords  = 16
	transcriptBudget = 24_000
	messageCapChars  = 1_500
	suggestSystem    = "You predict what the USER will type next into their coding agent CLI.\n" +
		"You are shown a transcript of the conversation so far. The agent's latest reply ends the transcript.\n\n" +
		"FIRST: look at the user's recent messages and original request.\n" +
		"Your job is to predict what THEY would type next — not what you think they should do.\n" +
		"THE TEST: would they think \"I was just about to type that\"?\n\n" +
		"EXAMPLES:\n" +
		"- User asked \"fix the bug and run tests\", bug is fixed -> \"run the tests\"\n" +
		"- After code was written -> \"try it out\"\n" +
		"- Agent offers options -> the option the user would likely pick, based on the conversation\n" +
		"- Agent ends by asking a yes/no question (continue? delete it? print it?) -> the user's likely answer: \"yes\" or \"no\"\n" +
		"- Task complete with an obvious follow-up -> \"commit this\" or \"push it\"\n" +
		"- After an error or a misunderstanding -> NONE (let them assess)\n\n" +
		"Be specific: \"run the tests\" beats \"continue\".\n" +
		"When the agent's reply ends with a question, a suggestion almost always exists — predict the answer.\n\n" +
		"NEVER SUGGEST:\n" +
		"- A message the user already sent, or a rephrasing of one — the transcript is history, not a menu. " +
		"Once a request was handled, predict the step AFTER it, never the request again " +
		"(short confirmations like \"yes\" or \"continue\" are the only acceptable repeats)\n" +
		"- Evaluative filler (\"looks good\", \"thanks\")\n" +
		"- Questions back to the agent (\"what about...?\")\n" +
		"- Agent-voice phrasing (\"Let me...\", \"I'll...\", \"Here's...\")\n" +
		"- New ideas the user never asked about\n" +
		"- Multiple sentences\n\n" +
		"Stay silent if the next step is not obvious from what the user said: reply with the single word NONE.\n\n" +
		"Format: 2-12 words, matching the user's own style and casing.\n" +
		"Reply with ONLY the suggestion text (or NONE) — no quotes, no markdown, no explanation."
)

type suggestionLoadedMsg struct {
	text       string
	generation uint64
}

// promptSuggestion is the next-prompt ghost controller (Rust PromptSuggestionController).
type promptSuggestion struct {
	full       string
	generation uint64
	dismissed  bool
	enabled    bool
}

func (c *promptSuggestion) beginFetch() uint64 {
	c.generation++
	return c.generation
}

func (c *promptSuggestion) onLoaded(text string, generation uint64) bool {
	if generation != c.generation {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" || strings.Contains(text, "\n") {
		c.full = ""
		return false
	}
	c.full = text
	c.dismissed = false
	return true
}

func (c *promptSuggestion) ghostFor(text string) string {
	if !c.enabled || c.dismissed || c.full == "" {
		return ""
	}
	if !strings.HasPrefix(c.full, text) {
		return ""
	}
	rest := strings.TrimPrefix(c.full, text)
	return rest
}

func (c *promptSuggestion) accept(text string) string {
	rest := c.ghostFor(text)
	if rest == "" {
		return ""
	}
	c.clear()
	return rest
}

func (c *promptSuggestion) acceptWord(text string) string {
	rest := c.ghostFor(text)
	if rest == "" {
		return ""
	}
	end := oneWordEnd(rest)
	if end == 0 {
		return ""
	}
	word := rest[:end]
	if strings.TrimSpace(rest[end:]) == "" {
		c.clear()
	}
	return word
}

func (c *promptSuggestion) dismiss() { c.dismissed = true }

func (c *promptSuggestion) clear() {
	c.full = ""
	c.generation++
}

func (c *promptSuggestion) hasSuggestion() bool {
	return c.enabled && !c.dismissed && c.full != ""
}

func oneWordEnd(s string) int {
	i := 0
	for i < len(s) && unicode.IsSpace(rune(s[i])) {
		i++
	}
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return i
}

func promptSuggestionsEnabled(cfg config.File) bool {
	if config.EnvIsOff("ENTROPY_PROMPT_SUGGESTIONS", "GROK_PROMPT_SUGGESTIONS") {
		return false
	}
	if config.EnvIsOn("ENTROPY_PROMPT_SUGGESTIONS", "GROK_PROMPT_SUGGESTIONS") {
		return true
	}
	return cfg.PrefBool("prompt_suggestions", false, true)
}

func sanitizeSuggestion(raw string) string {
	line := strings.TrimSpace(raw)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	line = strings.Trim(line, "\"'`“”‘’")
	if line == "" || utf8.RuneCountInString(line) >= suggestMaxChars {
		return ""
	}
	lower := strings.ToLower(line)
	for _, meta := range []string{"none", "n/a", "no suggestion", "nothing", "(silence)", "silence", "null"} {
		if lower == meta || strings.HasPrefix(lower, meta+".") {
			return ""
		}
	}
	if strings.ContainsAny(line, "`*") || strings.HasPrefix(line, "#") {
		return ""
	}
	words := strings.Fields(line)
	if len(words) > suggestMaxWords {
		return ""
	}
	if len(words) <= 1 {
		allow := map[string]bool{
			"yes": true, "yeah": true, "yep": true, "no": true, "ok": true, "okay": true,
			"continue": true, "proceed": true, "push": true, "commit": true, "deploy": true,
			"stop": true, "check": true, "retry": true, "undo": true, "merge": true,
		}
		if !allow[lower] {
			return ""
		}
	}
	if strings.Contains(line, ". ") || strings.Contains(line, "? ") || strings.Contains(line, "! ") {
		return ""
	}
	for _, p := range []string{"let me", "i'll", "i will", "here's", "here is"} {
		if strings.HasPrefix(lower, p) {
			return ""
		}
	}
	return line
}

func buildSuggestTranscript(msgs []types.Message) string {
	var lines []string
	used := 0
	sawAgent := false
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) > messageCapChars {
			runes := []rune(text)
			text = string(runes[:messageCapChars])
		}
		var line string
		switch m.Role {
		case types.RoleUser:
			if strings.HasPrefix(text, "<system-reminder>") {
				continue
			}
			line = "User: " + text
		case types.RoleAssistant:
			line = "Agent: " + text
			sawAgent = true
		default:
			continue
		}
		if used+len(line) > transcriptBudget && len(lines) > 0 {
			break
		}
		used += len(line)
		lines = append(lines, line)
	}
	if !sawAgent || len(lines) == 0 {
		return ""
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n\n")
}

func (m *model) shouldFetchSuggestion(turnErr error) bool {
	if turnErr != nil {
		return false
	}
	if !m.suggest.enabled {
		return false
	}
	if len(m.queue) > 0 {
		return false
	}
	if strings.TrimSpace(m.ta.Value()) != "" {
		return false
	}
	if m.sess == nil {
		return false
	}
	return buildSuggestTranscript(m.sess.Messages) != ""
}

func (m *model) fetchPromptSuggestion() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	gen := m.suggest.beginFetch()
	transcript := buildSuggestTranscript(m.sess.Messages)
	if transcript == "" {
		return nil
	}
	cwd := m.opts.CWD
	model := m.opts.Model
	kimiCode := model.EnvKey == "KIMI_CODE_API_KEY"
	sessID := m.sess.ID
	user := "CWD: " + cwd + "\n\nTranscript:\n\n" + transcript + "\n\nPredict the user's next message. Reply with ONLY the suggestion text."
	return func() tea.Msg {
		bearer, _, err := auth.ResolveBearer(model.Provider, auth.EnvKeys(model.Provider), kimiCode)
		if err != nil {
			return suggestionLoadedMsg{generation: gen}
		}
		id := model.Model
		if id == "" {
			id = model.ID
		}
		ad := sampler.Adapter{
			Provider: model.Provider,
			Backend:  model.APIBackend,
			BaseURL:  strings.TrimRight(model.BaseURL, "/"),
			Bearer:   bearer,
		}
		ch, err := ad.Stream(context.Background(), types.SampleRequest{
			Model: id,
			Messages: []types.Message{
				{Role: types.RoleSystem, Content: suggestSystem},
				{Role: types.RoleUser, Content: user},
			},
			SessionID: sessID + "-suggest",
		})
		if err != nil {
			return suggestionLoadedMsg{generation: gen}
		}
		var raw strings.Builder
		for ev := range ch {
			if ev.Kind == types.StreamDelta {
				raw.WriteString(ev.Text)
			}
		}
		return suggestionLoadedMsg{text: sanitizeSuggestion(raw.String()), generation: gen}
	}
}

func (m *model) tryAcceptSuggestion(oneWord bool) bool {
	if m.focusScroll || m.overlay != overlayNone {
		return false
	}
	text := m.ta.Value()
	if m.suggest.ghostFor(text) == "" {
		return false
	}
	var rest string
	if oneWord {
		rest = m.suggest.acceptWord(text)
	} else {
		rest = m.suggest.accept(text)
	}
	if rest == "" {
		return false
	}
	m.ta.SetValue(text + rest)
	return true
}
