package pager

import (
	"strconv"
	"strings"
)

const jumpPreviewMaxChars = 120

// promptPreview is the first non-empty line, char-capped with an ellipsis.
// Matches scrollback/state/timeline.rs::prompt_preview.
func promptPreview(text string) string {
	line := ""
	for _, l := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			line = t
			break
		}
	}
	runes := []rune(line)
	if len(runes) <= jumpPreviewMaxChars {
		return line
	}
	return string(runes[:jumpPreviewMaxChars-1]) + "…"
}

func (m *model) userTurnBlocks() []int {
	var idx []int
	for i, b := range m.blocks {
		if b.kind == blockUser {
			idx = append(idx, i)
		}
	}
	return idx
}

// openJump opens the /jump picker. Cursor motion live-scrolls the transcript;
// Enter keeps the jump; Esc restores the viewport captured here.
func (m *model) openJump() {
	m.overlay = overlayTimeline
	m.filter = ""
	m.jumpRestore = m.vp.YOffset
	m.jumpRestoreSet = true
	m.items = nil
	turns := m.userTurnBlocks()
	ordW := len(strconv.Itoa(max(1, len(turns))))
	for i, bi := range turns {
		preview := promptPreview(m.blocks[bi].body)
		if preview == "" {
			preview = "(no preview)"
		}
		ord := strconv.Itoa(i + 1)
		for len(ord) < ordW {
			ord = " " + ord
		}
		m.items = append(m.items, listItem{
			Key:   strconv.Itoa(bi),
			Label: ord + " " + preview,
		})
	}
	m.cursor = 0
	if n := len(m.items); n > 0 {
		m.cursor = n - 1
	}
	m.previewJump()
}

func (m *model) previewJump() {
	if m.overlay != overlayTimeline || m.cursor >= len(m.items) {
		return
	}
	idx, err := strconv.Atoi(m.items[m.cursor].Key)
	if err != nil {
		return
	}
	m.jumpToBlock(idx)
}

func (m *model) restoreJumpViewport() {
	if !m.jumpRestoreSet {
		return
	}
	m.vp.SetYOffset(m.jumpRestore)
	m.jumpRestoreSet = false
}

func (m *model) commitJump() {
	m.previewJump()
	m.jumpRestoreSet = false
	m.overlay = overlayNone
	m.filter = ""
}
