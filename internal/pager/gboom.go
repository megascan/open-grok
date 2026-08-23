package pager

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/gboom"
)

// Matches crates/codegen/xai-grok-pager/src/app/dispatch/status.rs.
const gboomNoGraphicsToast = "No demons here \u2014 GBOOM needs a graphics-capable terminal " +
	"(kitty, Ghostty, WezTerm, iTerm2)"

const gboomFPS = time.Second / 30

var gboomRed = lipgloss.NewStyle().Foreground(lipgloss.Color("#eb281f")).Bold(true)

type gboomTickMsg time.Time

func gboomTick() tea.Cmd {
	return tea.Tick(gboomFPS, func(t time.Time) tea.Msg { return gboomTickMsg(t) })
}

func detectGraphicsProtocol() bool {
	if os.Getenv("KITTY_WINDOW_ID") != "" || os.Getenv("ITERM_SESSION_ID") != "" {
		return true
	}
	term := strings.ToLower(os.Getenv("TERM"))
	if strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") || strings.Contains(term, "wezterm") {
		return true
	}
	switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
	case "iterm.app", "wezterm", "ghostty", "kitty":
		return true
	}
	return false
}

func detectKittyKeyboard() bool {
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	return strings.Contains(strings.ToLower(os.Getenv("TERM")), "kitty")
}

func (m *model) openGboom() tea.Cmd {
	st := gboom.NewState()
	st.SetReleaseAware(detectKittyKeyboard())
	m.overlay = overlayGboom
	m.gboom = st
	return gboomTick()
}

func (m *model) closeGboom() {
	m.overlay = overlayNone
	if m.gboom != nil {
		m.gboom.ReleaseAll()
	}
	m.gboom = nil
}

func (m *model) handleGboomTick() tea.Cmd {
	if m.overlay != overlayGboom || m.gboom == nil {
		return nil
	}
	m.gboom.Tick()
	return gboomTick()
}

func (m *model) handleGboomKey(msg tea.KeyMsg) tea.Cmd {
	if m.gboom == nil {
		m.overlay = overlayNone
		return nil
	}
	switch m.gboom.HandleKey(msg.String()) {
	case gboom.KeyClose:
		m.closeGboom()
	}
	return nil
}

func (m *model) handleGboomMouse(msg tea.MouseMsg) tea.Cmd {
	if m.gboom == nil {
		return nil
	}
	kind := gboom.MouseMoved
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		kind = gboom.MouseClick
	case msg.Action == tea.MouseActionMotion:
		kind = gboom.MouseMoved
	default:
		if msg.Button != 0 && msg.Action != tea.MouseActionRelease {
			kind = gboom.MouseDrag
		} else if msg.Action != tea.MouseActionMotion {
			return nil
		}
	}
	m.gboom.HandleMouse(kind, msg.X, msg.Y)
	return nil
}

func (m *model) gboomPopupSize() (w, h int) {
	w = max(30, m.width*90/100)
	h = max(8, m.height*90/100)
	if w > m.width {
		w = m.width
	}
	if h > m.height {
		h = m.height
	}
	return w, h
}

func (m *model) renderGboom() string {
	w, h := m.gboomPopupSize()
	hud := gboom.HUD{HP: 100, Total: 8}
	if m.gboom != nil {
		hud = m.gboom.HUD()
	}
	title := gboomRed.Render(" GBOOM ")
	innerW := max(1, w-2)
	innerH := max(1, h-2)
	frame := ""
	if m.gboom != nil {
		fw, fh := gboom.FrameSizeForCells(innerW, innerH)
		pix := m.gboom.FramePixels(fw, fh)
		frame = renderHalfBlocks(pix, fw, fh, innerW, innerH)
		px := max(0, (m.width-w)/2)
		py := max(0, (m.height-h)/3)
		m.gboom.SetMouseRegion(px, py, w, h)
	}
	if frame == "" {
		frame = strings.Repeat("\n", max(1, innerH-2))
		frame += dimStyle.Render("KNEE-DEEP IN THE TOKENS")
	}
	hpStyle := okStyle
	if hud.HP <= 30 {
		hpStyle = errStyle
	} else if hud.HP <= 60 {
		hpStyle = warnStyle
	}
	stats := hpStyle.Bold(true).Render(fmt.Sprintf(" HP %-3d \u00b7 KILLS %d/%d ", hud.HP, hud.Kills, hud.Total))
	hint := " ESC quit "
	if hud.Playing {
		hint = " WASD/\u2190\u2192 move \u00b7 SPACE fire \u00b7 ESC quit "
	}
	gap := max(1, w-4-lipgloss.Width(stats)-len([]rune(hint)))
	bar := stats + strings.Repeat(" ", gap) + dimStyle.Render(hint)
	body := lipgloss.PlaceHorizontal(w-2, lipgloss.Center, title) + "\n" + frame + "\n" + bar
	return modalBox.Width(w).Render(body)
}

func renderHalfBlocks(pix []byte, fw, fh, cols, rows int) string {
	if fw < 1 || fh < 1 || cols < 1 || rows < 1 || len(pix) < fw*fh*3 {
		return ""
	}
	var b strings.Builder
	for row := 0; row < rows; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		y0 := row * fh / rows
		y1 := (row*2 + 1) * fh / (rows * 2)
		if y1 <= y0 {
			y1 = y0
		}
		if y1 >= fh {
			y1 = fh - 1
		}
		var last string
		for col := 0; col < cols; col++ {
			x := col * fw / cols
			if x >= fw {
				x = fw - 1
			}
			tr, tg, tb := sampleRGB(pix, fw, x, y0)
			br, bg, bb := sampleRGB(pix, fw, x, y1)
			seq := fmt.Sprintf("\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", tr, tg, tb, br, bg, bb)
			if seq != last {
				b.WriteString(seq)
				last = seq
			} else {
				b.WriteRune('▀')
			}
		}
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

func sampleRGB(pix []byte, w, x, y int) (uint8, uint8, uint8) {
	i := (y*w + x) * 3
	return pix[i], pix[i+1], pix[i+2]
}
