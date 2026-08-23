package pager

import (
	"strings"
	"unicode"
)

// nucleoMatch scores query against text the way the Rust pager's nucleo
// history search does: subsequence with bonuses for prefix, word starts,
// and consecutive runs. marks are rune indices in text (not bytes).
func nucleoMatch(query, text string) (score int, marks []int) {
	q := []rune(strings.ToLower(strings.TrimSpace(query)))
	if len(q) == 0 {
		return 1, nil
	}
	t := []rune(text)
	tl := make([]rune, len(t))
	for i, r := range t {
		tl[i] = unicode.ToLower(r)
	}
	qi := 0
	run := 0
	consecutive := 0
	wordStart := 0
	for i, r := range tl {
		if qi >= len(q) {
			break
		}
		if r != q[qi] {
			run = 0
			continue
		}
		marks = append(marks, i)
		bonus := 1
		if i == 0 || nucleoWordStart(t, i) {
			bonus += 8
			wordStart++
		}
		if run > 0 {
			bonus += 12 + run*2
			consecutive++
		}
		if qi == 0 && i == 0 {
			bonus += 20
		}
		score += bonus
		qi++
		run++
	}
	if qi < len(q) {
		return 0, nil
	}
	// Prefer shorter gaps and earlier matches.
	score += 40
	score -= (len(t) - len(q)) / 4
	if string(tl) == string(q) {
		score += 1000
	} else if strings.HasPrefix(string(tl), string(q)) {
		score += 400
	}
	_ = consecutive
	_ = wordStart
	if score < 1 {
		score = 1
	}
	return score, marks
}

func nucleoWordStart(runes []rune, i int) bool {
	if i == 0 {
		return true
	}
	prev := runes[i-1]
	return unicode.IsSpace(prev) || prev == '/' || prev == '-' || prev == '_' || prev == '.' || prev == ':'
}

func highlightMarks(s string, marks []int, on, off func(string) string) string {
	if len(marks) == 0 || on == nil {
		return s
	}
	hit := map[int]bool{}
	for _, i := range marks {
		hit[i] = true
	}
	var b strings.Builder
	in := false
	for i, r := range []rune(s) {
		want := hit[i]
		if want && !in {
			in = true
		} else if !want && in {
			in = false
		}
		ch := string(r)
		if want {
			b.WriteString(on(ch))
		} else if off != nil {
			b.WriteString(off(ch))
		} else {
			b.WriteString(ch)
		}
	}
	return b.String()
}
