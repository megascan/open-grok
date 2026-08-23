package pager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/config"
)

// Session-event copy matches crates/codegen/xai-grok-pager/src/scrollback/blocks/session_event.rs.
const (
	emDash              = "\u2014"
	rightArrow          = "\u2192"
	middleDot           = "\u00b7"
	diskFullUserMessage = "Out of disk space. Free some space and try again."
	reAuthRequiredMsg   = "Authentication required " + emDash + " your session has expired or your " +
		"credentials were rejected. Run /login to re-authenticate, then resend " +
		"your message."
	contextTooLargeMsg = "This conversation is too large for the model's context window. " +
		"Use /new to start a new session."
	encryptedHistoryMsg = "This session's conversation history is incompatible with the " +
		"current model. Please start a new session."
)

type sessionKind int

const (
	sessTurnCompleted sessionKind = iota
	sessTurnCancelled
	sessTurnHalted
	sessTurnFailed
	sessCompactionStarted
	sessCompactionCompleted
	sessCompactionFailed
	sessCompactionCancelled
	sessRetryFailed
	sessRequestFailed
	sessReAuthRequired
	sessContextTooLarge
	sessDiskFull
	sessCompactCompleted
	sessHookAnnotation
	sessModelUnavailable
	sessMemorySaved
	sessGoalCompleted
	sessRecap
)

type sessionEvent struct {
	kind         sessionKind
	elapsed      time.Duration
	elapsedOK    bool
	error        string
	percentage   uint8
	tokensBefore *uint64
	tokensAfter  uint64
	elapsedMS    *int64
	errorType    string
	status       *uint16
	headline     string
	detail       string
	note         string
	prevModel    string
	newModel     string
	path         string
	trigger      string
	summary      string
	auto         bool
	running      bool
}

func (e sessionEvent) isWarning() bool {
	switch e.kind {
	case sessReAuthRequired, sessContextTooLarge, sessDiskFull,
		sessCompactionFailed, sessRequestFailed, sessRetryFailed, sessTurnFailed:
		return true
	default:
		return false
	}
}

func (e sessionEvent) isTurnTerminal() bool {
	switch e.kind {
	case sessTurnCompleted, sessTurnCancelled, sessTurnHalted, sessTurnFailed:
		return true
	default:
		return false
	}
}

func (e sessionEvent) recapHasBody() bool {
	return e.kind == sessRecap && strings.TrimSpace(e.summary) != ""
}

func (e sessionEvent) message() string {
	switch e.kind {
	case sessTurnCompleted:
		if e.elapsedOK {
			return "Worked for " + formatDuration(e.elapsed)
		}
		return "Turn completed."
	case sessTurnCancelled:
		return "Turn cancelled by user in " + formatDuration(e.elapsed) + "."
	case sessTurnHalted:
		return "Agent was unable to make progress " + emDash + " turn ended in " + formatDuration(e.elapsed) + "."
	case sessTurnFailed:
		if e.elapsedOK {
			return "Turn failed in " + formatDuration(e.elapsed) + ": " + e.error
		}
		return "Turn failed: " + e.error
	case sessCompactionStarted:
		return fmt.Sprintf("Context %d%% full. Compacting…", e.percentage)
	case sessCompactionCompleted:
		after := formatEventTokens(e.tokensAfter)
		body := "Context compacted → " + after + " tokens"
		if e.tokensBefore != nil && *e.tokensBefore > 0 {
			body = "Context compacted: " + formatEventTokens(*e.tokensBefore) + " → " + after + " tokens"
		}
		if e.elapsedMS != nil {
			secs := float64(*e.elapsedMS) / 1000.0
			return fmt.Sprintf("%s (%.1fs)", body, secs)
		}
		return body
	case sessCompactionFailed:
		if strings.TrimSpace(e.error) == "" {
			return "Compaction failed."
		}
		return "Compaction failed: " + e.error
	case sessCompactionCancelled:
		return "Compaction cancelled."
	case sessRetryFailed:
		if e.errorType == "encrypted_content_mismatch" {
			return encryptedHistoryMsg
		}
		return "Retry failed: " + e.error
	case sessRequestFailed:
		return bannerMessage(e.headline, e.detail)
	case sessReAuthRequired:
		return reAuthRequiredMsg
	case sessContextTooLarge:
		return contextTooLargeMsg
	case sessDiskFull:
		return diskFullUserMessage
	case sessCompactCompleted:
		return "Compaction completed in " + formatDuration(e.elapsed) + "."
	case sessHookAnnotation:
		return e.note
	case sessModelUnavailable:
		if e.newModel == "" {
			return e.note
		}
		return e.note + ` Switched to "` + e.newModel + `".`
	case sessMemorySaved:
		short := abbreviatePath(e.path)
		return "Memory saved (" + e.trigger + ") " + rightArrow + " " + short + "  " + middleDot + "  /memory to view"
	case sessGoalCompleted:
		return "Goal complete " + emDash + " " + formatDuration(e.elapsed) + " end-to-end."
	case sessRecap:
		return "Recap " + emDash + " " + e.summary
	default:
		return e.note
	}
}

func bannerMessage(headline, detail string) string {
	if detail == "" {
		return headline
	}
	return headline + " " + emDash + " " + detail
}

func formatEventTokens(tokens uint64) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000.0)
	}
	return strconv.FormatUint(tokens, 10)
}

// formatDuration matches pager-render util::format_duration: 5.2s, 32s, 2m5s, 1h2m.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSecs := int64(d / time.Second)
	if totalSecs < 10 {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if totalSecs < 60 {
		return fmt.Sprintf("%ds", totalSecs)
	}
	mins := totalSecs / 60
	secs := totalSecs % 60
	if mins < 60 {
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	hours := mins / 60
	remain := mins % 60
	return fmt.Sprintf("%dh%dm", hours, remain)
}

func abbreviatePath(path string) string {
	home := config.Home()
	if rest, ok := stripPathPrefix(path, home); ok {
		prefix := "~/.entropy"
		if home != config.DefaultHome() {
			prefix = "$ENTROPY_HOME"
		}
		if rest == "" {
			return prefix
		}
		return prefix + "/" + rest
	}
	if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
		if rest, ok := stripPathPrefix(path, userHome); ok {
			if rest == "" {
				return "~"
			}
			return "~/" + rest
		}
	}
	return path
}

func stripPathPrefix(path, prefix string) (string, bool) {
	clean := filepath.Clean(path)
	pref := filepath.Clean(prefix)
	if clean == pref {
		return "", true
	}
	sep := string(filepath.Separator)
	if strings.HasPrefix(clean, pref+sep) {
		return filepath.ToSlash(strings.TrimPrefix(clean, pref+sep)), true
	}
	return "", false
}

func sessionEventFromTurn(err error, elapsed *time.Duration) sessionEvent {
	elapsedDur := time.Duration(0)
	elapsedOK := false
	if elapsed != nil {
		elapsedDur = *elapsed
		elapsedOK = true
	}
	if err == nil {
		return sessionEvent{kind: sessTurnCompleted, elapsed: elapsedDur, elapsedOK: elapsedOK}
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	if err == context.Canceled || strings.Contains(low, "canceled") || strings.Contains(low, "cancelled") {
		return sessionEvent{kind: sessTurnCancelled, elapsed: elapsedDur, elapsedOK: true}
	}
	if strings.Contains(low, "encrypted_content_mismatch") {
		return sessionEvent{kind: sessRetryFailed, error: msg, errorType: "encrypted_content_mismatch"}
	}
	if isAuthTurnError(low) {
		return sessionEvent{kind: sessReAuthRequired}
	}
	if isContextTooLargeError(low) {
		return sessionEvent{kind: sessContextTooLarge}
	}
	if isDiskFullError(low) {
		return sessionEvent{kind: sessDiskFull}
	}
	if strings.Contains(low, "unable to make progress") ||
		strings.Contains(low, "max tool turns") ||
		strings.Contains(low, "halted") ||
		strings.Contains(low, "doom") {
		return sessionEvent{kind: sessTurnHalted, elapsed: elapsedDur, elapsedOK: true}
	}
	if strings.Contains(low, "retry failed") || strings.Contains(low, "retries exhausted") {
		return sessionEvent{kind: sessRetryFailed, error: msg}
	}
	if ev, ok := requestFailedFromError(msg, low); ok {
		return ev
	}
	return sessionEvent{kind: sessTurnFailed, error: msg, elapsed: elapsedDur, elapsedOK: elapsedOK}
}

func isAuthTurnError(low string) bool {
	return strings.Contains(low, "unauthorized") ||
		strings.Contains(low, "401") ||
		strings.Contains(low, "authentication") ||
		strings.Contains(low, "invalid api key") ||
		strings.Contains(low, "invalid_api_key") ||
		strings.Contains(low, "credentials were rejected")
}

func isContextTooLargeError(low string) bool {
	return strings.Contains(low, "context_length") ||
		strings.Contains(low, "context length") ||
		strings.Contains(low, "too large for the model") ||
		strings.Contains(low, "max_prompt") ||
		(strings.Contains(low, "context") && strings.Contains(low, "too large"))
}

func isDiskFullError(low string) bool {
	return strings.Contains(low, "enospc") ||
		strings.Contains(low, "no space left") ||
		(strings.Contains(low, "disk") && (strings.Contains(low, "full") || strings.Contains(low, "space")))
}

func requestFailedFromError(msg, low string) (sessionEvent, bool) {
	if !strings.Contains(low, "status") && !strings.Contains(low, "server error") && !strings.Contains(low, "http") {
		return sessionEvent{}, false
	}
	for code := 500; code <= 599; code++ {
		s := strconv.Itoa(code)
		if strings.Contains(msg, s) {
			st := uint16(code)
			return sessionEvent{
				kind:     sessRequestFailed,
				status:   &st,
				headline: fmt.Sprintf("Server error (%d)", code),
				detail:   msg,
			}, true
		}
	}
	return sessionEvent{}, false
}

func (m *model) appendSessionEvent(ev sessionEvent) {
	bl := block{
		kind:  blockSession,
		event: ev,
		body:  ev.message(),
		err:   ev.isWarning(),
		at:    time.Now(),
	}
	m.appendBlock(bl)
}

func (m *model) renderSessionEvent(bl block, w int) string {
	ev := bl.event
	if ev.kind == sessRecap {
		return renderRecapEvent(ev, bl.folded, w)
	}
	text := ev.message()
	style := dimStyle
	if ev.isWarning() {
		style = warnStyle
	}
	lines := wrapSessionLines(text, max(8, w-4))
	if len(lines) == 0 {
		lines = []string{""}
	}
	if !ev.isWarning() {
		var b strings.Builder
		for i, line := range lines {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(style.Render(line))
		}
		return b.String()
	}
	var b strings.Builder
	for _, line := range lines {
		writeAccentWith(&b, warnStyle, style.Render(line))
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderRecapEvent(ev sessionEvent, folded bool, w int) string {
	header := titleStyle.Bold(true).Render("Recap")
	if ev.running {
		var b strings.Builder
		writeAccentWith(&b, dimStyle, header)
		return strings.TrimRight(b.String(), "\n")
	}
	summary := strings.TrimSpace(ev.summary)
	if folded || !ev.recapHasBody() {
		line := header
		preview := ""
		if summary != "" {
			preview = strings.TrimSpace(strings.Split(summary, "\n")[0])
		}
		if preview != "" {
			line += dimStyle.Render("  " + preview)
		}
		_ = w
		return line
	}
	var b strings.Builder
	writeAccentWith(&b, toolStyle, header)
	writeAccentWith(&b, toolStyle, "")
	bodyW := max(20, w-4)
	for _, line := range wrapSessionLines(summary, bodyW) {
		writeAccentWith(&b, toolStyle, dimStyle.Render(line))
	}
	return strings.TrimRight(b.String(), "\n")
}

func wrapSessionLines(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		var line strings.Builder
		for _, word := range words {
			if line.Len() == 0 {
				line.WriteString(word)
				continue
			}
			if lipgloss.Width(line.String())+1+lipgloss.Width(word) > w {
				out = append(out, line.String())
				line.Reset()
				line.WriteString(word)
				continue
			}
			line.WriteByte(' ')
			line.WriteString(word)
		}
		if line.Len() > 0 {
			out = append(out, line.String())
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func writeAccentWith(b *strings.Builder, bar lipgloss.Style, s string) {
	b.WriteString(bar.Render(permAccentBar) + "  " + s + "\n")
}
