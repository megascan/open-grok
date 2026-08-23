package pager

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MAX_TITLE_SCALARS matches xai-grok-shell session persistence.
const maxTitleScalars = 100

func isForbiddenTitleChar(c rune) bool {
	if unicode.IsControl(c) {
		return true
	}
	switch c {
	case '\u200E', '\u200F',
		'\u202A', '\u202B', '\u202C', '\u202D', '\u202E',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	}
	return false
}

// sanitizeRenameTitle drops C0/C1 and bidi/format controls, then trims.
func sanitizeRenameTitle(title string) string {
	if strings.IndexFunc(title, isForbiddenTitleChar) < 0 {
		return strings.TrimSpace(title)
	}
	var b strings.Builder
	b.Grow(len(title))
	for _, c := range title {
		if !isForbiddenTitleChar(c) {
			b.WriteRune(c)
		}
	}
	return strings.TrimSpace(b.String())
}

func isAutoVerb(args string) bool {
	return strings.TrimSpace(args) == "--auto"
}

func autoVerbHasTrailingText(args string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(args), "--auto")
	return ok && rest != "" && unicode.IsSpace(rune(rest[0]))
}

func parseRenameArg(arg string) (title string, auto bool, errMsg string) {
	cleaned := sanitizeRenameTitle(arg)
	if isAutoVerb(cleaned) || isAutoVerb(arg) {
		return "", true, ""
	}
	if autoVerbHasTrailingText(arg) || autoVerbHasTrailingText(cleaned) {
		return "", false, "--auto takes no title"
	}
	if cleaned == "" {
		return "", false, "Usage: /rename <new title> | --auto"
	}
	if utf8.RuneCountInString(cleaned) > maxTitleScalars {
		return "", false, "title too long (max 100 characters)"
	}
	return cleaned, false, ""
}
