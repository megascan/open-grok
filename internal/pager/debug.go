package pager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/config"
)

const fpsPanelWidth = 32

func envFlagOn(names ...string) bool {
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok {
			return v != "" && v != "0"
		}
	}
	return false
}

func envScrollLogStart() (string, bool) {
	for _, n := range []string{"ENTROPY_SCROLL_LOG", "GROK_SCROLL_LOG"} {
		v, ok := os.LookupEnv(n)
		if !ok {
			continue
		}
		if v == "0" {
			return "", false
		}
		if v == "" || v == "1" {
			return defaultScrollLogPath(), true
		}
		return v, true
	}
	return "", false
}

func defaultScrollLogPath() string {
	ts := time.Now().UTC().Format("20060102-150405")
	dir := filepath.Join(config.Home(), "logs")
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, "scroll-log-"+ts+".jsonl")
}

func debugOnOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// debugStatusLine is the bare `/debug` transcript line. The middle dot and
// em dash are the Rust router literals (`\u00b7` / `\u2014`).
func (m *model) debugStatusLine() string {
	return fmt.Sprintf(
		"debug toggles: scroll %s \u00b7 fps %s \u00b7 log %s \u2014 toggle with /debug <scroll|fps|log>",
		debugOnOff(m.scrollHUD),
		debugOnOff(m.fpsOn),
		debugOnOff(m.scrollLogPath != ""),
	)
}

func (m *model) toggleFPSHud() {
	m.fpsOn = !m.fpsOn
	m.fpsSamples = nil
	m.fpsBody = ""
	m.fpsRefresh = time.Time{}
	m.lastViewAt = time.Time{}
}

func (m *model) toggleScrollLog() string {
	if m.scrollLogPath != "" {
		m.scrollLogPath = ""
		return "scroll log: off"
	}
	m.scrollLogPath = defaultScrollLogPath()
	return "scroll log: recording to " + m.scrollLogPath
}

func (m *model) handleDebug(arg string) {
	switch strings.TrimSpace(strings.ToLower(arg)) {
	case "":
		m.appendSys(m.debugStatusLine())
	case "scroll":
		m.scrollHUD = !m.scrollHUD
	case "fps":
		m.toggleFPSHud()
	case "log":
		m.appendSys(m.toggleScrollLog())
	default:
		m.appendSys(fmt.Sprintf("Unknown /debug option '%s'. Usage: /debug [scroll|fps|log]", strings.TrimSpace(arg)))
	}
}

func (m *model) recordFPS(d time.Duration) {
	if !m.fpsOn {
		return
	}
	m.fpsSamples = append(m.fpsSamples, d)
	if len(m.fpsSamples) > 120 {
		m.fpsSamples = m.fpsSamples[len(m.fpsSamples)-120:]
	}
}

func formatFPSStats(samples []time.Duration) string {
	if len(samples) == 0 {
		return "fps:- p50:- p95:-"
	}
	ms := make([]float64, len(samples))
	sum := 0.0
	for i, d := range samples {
		v := d.Seconds() * 1000
		ms[i] = v
		sum += v
	}
	sort.Float64s(ms)
	mean := sum / float64(len(ms))
	fps := 0.0
	if mean > 1e-6 {
		fps = 1000.0 / mean
	}
	return fmt.Sprintf("fps:%.0f p50:%.1fms p95:%.1fms", fps, percentile(ms, 50), percentile(ms, 95))
}

func percentile(sorted []float64, pct float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := (pct / 100.0) * float64(len(sorted)-1)
	lower := int(rank)
	if lower+1 >= len(sorted) {
		return sorted[lower]
	}
	frac := rank - float64(lower)
	return sorted[lower] + (sorted[lower+1]-sorted[lower])*frac
}

func (m *model) renderFPSPanel() string {
	if !m.fpsOn {
		return ""
	}
	if m.fpsRefresh.IsZero() || time.Since(m.fpsRefresh) >= 250*time.Millisecond {
		m.fpsBody = formatFPSStats(m.fpsSamples)
		m.fpsRefresh = time.Now()
	}
	title := padDebugLine("fps debug  (/debug fps)", fpsPanelWidth)
	body := padDebugLine(m.fpsBody, fpsPanelWidth)
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Background(lipgloss.Color("0"))
	white := lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("0"))
	return yellow.Render(title) + "\n" + white.Render(body)
}

func padDebugLine(s string, w int) string {
	runes := []rune(s)
	if len(runes) > w {
		return string(runes[:w])
	}
	return s + strings.Repeat(" ", w-len(runes))
}

func (m *model) logScroll(evt, trigger string, delta int) {
	if m.scrollLogPath == "" {
		return
	}
	rec := map[string]any{
		"evt":     evt,
		"trigger": trigger,
		"delta":   delta,
		"y":       m.vp.YOffset,
		"ts_ms":   time.Now().UnixMilli(),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(m.scrollLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		m.scrollLogPath = ""
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
}
