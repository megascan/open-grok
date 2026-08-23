package pager

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	timelineRailWidthCols = 2
	timelineMinWidth      = 60
	timelineMinTurns      = 2
)

type timelineHit int

const (
	timelineHitNone timelineHit = iota - 3
	timelineHitUp
	timelineHitDown
	// timelineHit >= 0 is a turn index in the window
)

type timelineRail struct {
	x, y, h, w           int
	windowStart          int
	windowLen            int
	ticksY               int
	upY, downY           int
	active               int
	upTarget, downTarget int
}

func (m *model) timelineRailWidth() int {
	if m.screenMd == modeMinimal {
		return 0
	}
	if !m.cfg.UI.ShowTimeline {
		return 0
	}
	if m.width < timelineMinWidth {
		return 0
	}
	if len(m.userTurnBlocks()) < timelineMinTurns {
		return 0
	}
	return timelineRailWidthCols
}

func (m *model) computeTimelineRail() *timelineRail {
	if m.timelineRailWidth() == 0 || m.vp.Height < 3 {
		m.lastRail = nil
		return nil
	}
	turns := m.userTurnBlocks()
	n := len(turns)
	height := m.vp.Height
	maxTicks := height - 2
	if maxTicks < 1 {
		m.lastRail = nil
		return nil
	}
	start, end := 0, n
	if n > maxTicks {
		atBottom := m.vp.AtBottom()
		active := m.activeTurnIndex(turns)
		tailStart := n - maxTicks
		if atBottom {
			if active >= 0 {
				start = min(active, tailStart)
			} else {
				start = tailStart
			}
		} else {
			if active < 0 {
				active = n - 1
			}
			start = active - maxTicks/2
			if start < 0 {
				start = 0
			}
			if start > tailStart {
				start = tailStart
			}
		}
		end = start + maxTicks
	}
	totalRows := (end - start) + 2
	top := (height - totalRows) / 2
	if top < 0 {
		top = 0
	}
	active := m.activeTurnIndex(turns)
	upTarget, downTarget := -1, -1
	if active > 0 {
		upTarget = active - 1
	}
	if active >= 0 && active+1 < n {
		downTarget = active + 1
	} else if active < 0 && n > 0 {
		downTarget = 0
	}
	rail := &timelineRail{
		x:           0,
		y:           m.transcriptTop,
		h:           height,
		w:           timelineRailWidthCols,
		windowStart: start,
		windowLen:   end - start,
		ticksY:      top + 1,
		upY:         top,
		downY:       top + 1 + (end - start),
		active:      active,
		upTarget:    upTarget,
		downTarget:  downTarget,
	}
	m.lastRail = rail
	return rail
}

func (m *model) activeTurnIndex(turns []int) int {
	if len(turns) == 0 {
		return -1
	}
	off := m.vp.YOffset
	best := 0
	for i, bi := range turns {
		if m.blockLineOffset(bi) <= off {
			best = i
		}
	}
	return best
}

func (m *model) renderTimelineRail() string {
	rail := m.computeTimelineRail()
	if rail == nil {
		return ""
	}
	rows := make([]string, rail.h)
	for i := range rows {
		rows[i] = "  "
	}
	up := "\u25b4"   // ▴
	down := "\u25be" // ▾
	upStyle, downStyle := dimStyle, dimStyle
	if rail.upTarget >= 0 {
		upStyle = dimStyle.Foreground(lipgloss.Color("252"))
		if m.railHover == timelineHitUp {
			upStyle = titleStyle
		}
	}
	if rail.downTarget >= 0 {
		downStyle = dimStyle.Foreground(lipgloss.Color("252"))
		if m.railHover == timelineHitDown {
			downStyle = titleStyle
		}
	}
	if rail.upY >= 0 && rail.upY < len(rows) {
		rows[rail.upY] = " " + upStyle.Render(up)
	}
	if rail.downY >= 0 && rail.downY < len(rows) {
		rows[rail.downY] = " " + downStyle.Render(down)
	}
	idle := " \u2500"            //  ─
	activeTick := "\u2501\u2501" // ━━
	hoverTick := "\u2500\u2500"  // ──
	for i := 0; i < rail.windowLen; i++ {
		y := rail.ticksY + i
		if y < 0 || y >= len(rows) {
			continue
		}
		turn := rail.windowStart + i
		hit := timelineHit(turn)
		switch {
		case rail.active == turn:
			rows[y] = titleStyle.Render(activeTick)
		case m.railHover == hit:
			rows[y] = titleStyle.Render(hoverTick)
		default:
			rows[y] = dimStyle.Render(idle)
		}
	}
	return strings.Join(rows, "\n")
}

func (r *timelineRail) hit(col, row int) timelineHit {
	if r == nil {
		return timelineHitNone
	}
	if col < r.x || col >= r.x+r.w {
		return timelineHitNone
	}
	rel := row - r.y
	if rel < 0 || rel >= r.h {
		return timelineHitNone
	}
	if rel == r.upY {
		return timelineHitUp
	}
	if rel == r.downY {
		return timelineHitDown
	}
	if rel >= r.ticksY {
		idx := rel - r.ticksY
		if idx >= 0 && idx < r.windowLen {
			return timelineHit(r.windowStart + idx)
		}
	}
	return timelineHitNone
}

func (m *model) jumpTimelineHit(hit timelineHit) {
	turns := m.userTurnBlocks()
	target := -1
	switch hit {
	case timelineHitNone:
		return
	case timelineHitUp:
		target = -1
		if m.lastRail != nil {
			target = m.lastRail.upTarget
		}
	case timelineHitDown:
		target = -1
		if m.lastRail != nil {
			target = m.lastRail.downTarget
		}
	default:
		target = int(hit)
	}
	if target < 0 || target >= len(turns) {
		return
	}
	m.jumpToBlock(turns[target])
}
