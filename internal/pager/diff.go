package pager

import (
	"fmt"
	"strings"
)

type changeTag int

const (
	tagEqual changeTag = iota
	tagInsert
	tagDelete
)

type diffLine struct {
	Text string
	Lo   int // 1-based old-file line; 0 when insert-only
	Ln   int // 1-based new-file line; 0 when delete-only
	Tag  changeTag
}

const (
	beginPatch = "*** Begin Patch"
	endPatch   = "*** End Patch"
	addFile    = "*** Add File: "
	deleteFile = "*** Delete File: "
	updateFile = "*** Update File: "
	moveTo     = "*** Move to: "
	lcsBudget  = 1_000_000
)

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []string{""}
	}
	return strings.Split(s, "\n")
}

func countInsDel(lines []diffLine) (ins, del int) {
	for _, l := range lines {
		switch l.Tag {
		case tagInsert:
			ins++
		case tagDelete:
			del++
		}
	}
	return
}

func countInsDelHunks(hunks [][]diffLine) (ins, del int) {
	for _, h := range hunks {
		i, d := countInsDel(h)
		ins += i
		del += d
	}
	return
}

func hunkFromWrite(contents string) []diffLine {
	lines := splitDiffLines(contents)
	out := make([]diffLine, len(lines))
	for i, t := range lines {
		out[i] = diffLine{Text: t, Ln: i + 1, Tag: tagInsert}
	}
	return out
}

func hunkFromReplace(oldS, newS string) []diffLine {
	return lcsDiff(splitDiffLines(oldS), splitDiffLines(newS), 1, 1)
}

func lcsDiff(a, b []string, lo0, ln0 int) []diffLine {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	if n > 0 && m > 0 && n*m > lcsBudget {
		return fallbackReplace(a, b, lo0, ln0)
	}
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []diffLine
	i, j := 0, 0
	lo, ln := lo0, ln0
	for i < n && j < m {
		if a[i] == b[j] {
			out = append(out, diffLine{Text: a[i], Lo: lo, Ln: ln, Tag: tagEqual})
			i++
			j++
			lo++
			ln++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			out = append(out, diffLine{Text: a[i], Lo: lo, Tag: tagDelete})
			i++
			lo++
			continue
		}
		out = append(out, diffLine{Text: b[j], Ln: ln, Tag: tagInsert})
		j++
		ln++
	}
	for i < n {
		out = append(out, diffLine{Text: a[i], Lo: lo, Tag: tagDelete})
		i++
		lo++
	}
	for j < m {
		out = append(out, diffLine{Text: b[j], Ln: ln, Tag: tagInsert})
		j++
		ln++
	}
	return out
}

func fallbackReplace(a, b []string, lo0, ln0 int) []diffLine {
	out := make([]diffLine, 0, len(a)+len(b))
	lo, ln := lo0, ln0
	for _, t := range a {
		out = append(out, diffLine{Text: t, Lo: lo, Tag: tagDelete})
		lo++
	}
	for _, t := range b {
		out = append(out, diffLine{Text: t, Ln: ln, Tag: tagInsert})
		ln++
	}
	return out
}

func hunkGap(prev, next []diffLine) int {
	var prevEnd, nextStart int
	for i := len(prev) - 1; i >= 0; i-- {
		if prev[i].Tag != tagDelete && prev[i].Ln > 0 {
			prevEnd = prev[i].Ln
			break
		}
	}
	for _, l := range next {
		if l.Tag != tagDelete && l.Ln > 0 {
			nextStart = l.Ln
			break
		}
	}
	if prevEnd == 0 || nextStart == 0 {
		return 0
	}
	gap := nextStart - prevEnd - 1
	if gap < 1 {
		return 0
	}
	return gap
}

func gutterWidth(hunk []diffLine) int {
	maxN := 1
	for _, l := range hunk {
		n := l.Ln
		if l.Tag == tagDelete {
			n = l.Lo
		}
		if n > maxN {
			maxN = n
		}
	}
	w := 1
	for maxN >= 10 {
		w++
		maxN /= 10
	}
	return w
}

func renderDiffHunks(hunks [][]diffLine) string {
	if len(hunks) == 0 {
		return ""
	}
	var b strings.Builder
	for i, hunk := range hunks {
		if i > 0 {
			gap := hunkGap(hunks[i-1], hunk)
			sep := "…"
			switch gap {
			case 0:
			case 1:
				sep = "… 1 unchanged line"
			default:
				sep = fmt.Sprintf("… %d unchanged lines", gap)
			}
			b.WriteString(dimStyle.Render("  "+sep) + "\n")
		}
		w := gutterWidth(hunk)
		for _, line := range hunk {
			num := line.Ln
			st := dimStyle
			switch line.Tag {
			case tagDelete:
				num = line.Lo
				st = deleteStyle
			case tagInsert:
				st = insertStyle
			}
			gutter := fmt.Sprintf("  %*d  ", w, num)
			text := line.Text
			if text == "" {
				text = " "
			}
			b.WriteString(dimStyle.Render(gutter) + st.Render(text) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type patchDisplay struct {
	Path      string
	Prefix    string
	Hunks     [][]diffLine
	Untrusted bool
	FileCount int
	Creating  bool
}

func parsePatchDisplay(patch string) patchDisplay {
	lines := strings.Split(strings.TrimSpace(patch), "\n")
	if len(lines) >= 4 && (lines[0] == "<<EOF" || lines[0] == "<<'EOF'" || lines[0] == "<<\"EOF\"") {
		lines = lines[1 : len(lines)-1]
	}
	out := patchDisplay{Prefix: "Edit "}
	if len(lines) < 2 {
		return out
	}
	start, end := 0, len(lines)
	for i, l := range lines {
		if strings.TrimSpace(l) == beginPatch {
			start = i + 1
			break
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == endPatch {
			end = i
			break
		}
	}
	i := start
	for i < end {
		line := strings.TrimRight(lines[i], "\r")
		switch {
		case strings.HasPrefix(line, addFile):
			path := strings.TrimPrefix(line, addFile)
			out.FileCount++
			if out.Path == "" {
				out.Path = path
				out.Prefix = "Creating "
				out.Creating = true
			}
			i++
			var neu []string
			for i < end && strings.HasPrefix(lines[i], "+") {
				neu = append(neu, lines[i][1:])
				i++
			}
			if len(neu) > 0 {
				out.Hunks = append(out.Hunks, hunkFromWrite(strings.Join(neu, "\n")))
			}
		case strings.HasPrefix(line, deleteFile):
			path := strings.TrimPrefix(line, deleteFile)
			out.FileCount++
			if out.Path == "" {
				out.Path = path
			}
			i++
		case strings.HasPrefix(line, updateFile):
			path := strings.TrimPrefix(line, updateFile)
			out.FileCount++
			if out.Path == "" {
				out.Path = path
			}
			i++
			if i < end && strings.HasPrefix(lines[i], moveTo) {
				i++
			}
			var oldB, newB []string
			flush := func() {
				if len(oldB) == 0 && len(newB) == 0 {
					return
				}
				out.Hunks = append(out.Hunks, lcsDiff(oldB, newB, 1, 1))
				oldB, newB = nil, nil
			}
			for i < end {
				l := lines[i]
				if strings.HasPrefix(l, "*** ") && !strings.HasPrefix(l, "*** End of File") {
					break
				}
				if strings.HasPrefix(l, "@@") || l == "*** End of File" {
					flush()
					i++
					continue
				}
				if len(l) == 0 {
					i++
					continue
				}
				switch l[0] {
				case ' ':
					oldB = append(oldB, l[1:])
					newB = append(newB, l[1:])
				case '-':
					oldB = append(oldB, l[1:])
				case '+':
					newB = append(newB, l[1:])
				}
				i++
			}
			flush()
		default:
			i++
		}
	}
	if out.FileCount > 1 {
		out.Untrusted = true
	}
	return out
}
