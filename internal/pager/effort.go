package pager

import (
	"fmt"
	"strings"

	"github.com/megascan/open-grok/internal/agent"
)

// Built-in fallback menu from slash/commands/effort_levels.rs (strongest first).
var effortLevels = []string{"xhigh", "high", "medium", "low"}

var effortAccepted = map[string]bool{
	"none": true, "minimal": true,
	"low": true, "medium": true, "high": true, "xhigh": true,
	"max": true, "ultra": true,
}

func effortUsage(current string) string {
	cur := ""
	if current != "" {
		cur = " (current: " + current + ")"
	}
	return fmt.Sprintf("Usage: /effort <%s>%s", strings.Join(effortLevels, "|"), cur)
}

// applyEffort sets sess.Effort. Returns (message, ok).
func applyEffort(sess *agent.Session, arg string) (string, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(arg))
	if trimmed == "" {
		return effortUsage(sess.Effort), false
	}
	if !effortAccepted[trimmed] {
		return fmt.Sprintf("unknown effort level '%s'; use one of: %s", trimmed, strings.Join(effortLevels, ", ")), false
	}
	sess.Effort = trimmed
	return "", true
}
