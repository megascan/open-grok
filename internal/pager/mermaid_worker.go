package pager

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/megascan/open-grok/internal/config"
	"github.com/zeebo/blake3"
)

// Hidden argv[1] the pager re-execs itself with to rasterize one diagram
// out of process, matching Rust MERMAID_RENDER_SUBCOMMAND.
const mermaidRenderSubcommand = "__mermaid-render"

const (
	mermaidLabel         = "\u25c7 mermaid"
	affordanceOpen       = "[Open Image]"
	affordanceCopyPath   = "[Copy Image Path]"
	affordanceCopySource = "[Copy Source]"
	mermaidRenderingHint = "rendering diagram\u2026"
	affordanceGap        = 3
	mermaidWidthBucket   = 8
	mermaidRenderRev     = 3
	mermaidRenderTimeout = 3 * time.Second
)

type mermaidClickKind int

const (
	mermaidClickOpen mermaidClickKind = iota
	mermaidClickCopyPath
	mermaidClickCopySource
)

type mermaidBtn struct {
	Label string
	Kind  mermaidClickKind
	Col   int
	Width int
}

type mermaidDoneMsg struct {
	hash   string
	path   string
	err    error
	action mermaidClickKind
}

func mermaidAffordanceLine(rendering bool) string {
	row, _ := layoutMermaidAffordance(rendering)
	return dimStyle.Render(row)
}

func layoutMermaidAffordance(rendering bool) (string, []mermaidBtn) {
	label := mermaidLabel
	col := utf8.RuneCountInString(label) + affordanceGap
	specs := []struct {
		label string
		kind  mermaidClickKind
	}{
		{affordanceOpen, mermaidClickOpen},
		{affordanceCopyPath, mermaidClickCopyPath},
		{affordanceCopySource, mermaidClickCopySource},
	}
	var b strings.Builder
	b.WriteString(label)
	b.WriteString(strings.Repeat(" ", affordanceGap))
	btns := make([]mermaidBtn, 0, 3)
	for i, s := range specs {
		if i > 0 {
			b.WriteString(strings.Repeat(" ", affordanceGap))
		}
		btns = append(btns, mermaidBtn{Label: s.label, Kind: s.kind, Col: col, Width: utf8.RuneCountInString(s.label)})
		b.WriteString(s.label)
		col += utf8.RuneCountInString(s.label) + affordanceGap
	}
	if rendering {
		b.WriteString(strings.Repeat(" ", affordanceGap))
		b.WriteString(mermaidRenderingHint)
	}
	return b.String(), btns
}

func hashMermaidSource(src string) string {
	sum := blake3.Sum256([]byte(src))
	return hex.EncodeToString(sum[:])
}

func mermaidCacheName(src string, dark bool, widthCols int, openTier bool) string {
	h := hashMermaidSource(src)
	theme := 0
	if dark {
		theme = 1
	}
	bucket := uint16(0xFFFF)
	tag := "o"
	if !openTier {
		if widthCols < 1 {
			widthCols = 1
		}
		bucket = uint16(widthCols / mermaidWidthBucket)
		tag = "t"
	}
	return fmt.Sprintf("%s-%d-%d-%s-r%d.png", h, theme, bucket, tag, mermaidRenderRev)
}

func mermaidCacheDir(sessionDir string) string {
	if sessionDir != "" {
		return filepath.Join(sessionDir, "mermaid")
	}
	return filepath.Join(config.Home(), "mermaid-cache")
}

func mermaidArtForPNG(src string, width int) string {
	kind, dir, nodes, edges, extras := parseMermaid(src)
	switch kind {
	case "sequencediagram":
		return renderSequenceASCII(extras, width)
	case "pie":
		return renderPieASCII(extras, width)
	case "flowchart", "graph":
		return renderFlowchartASCII(dir, nodes, edges, width)
	default:
		return strings.TrimSpace(src)
	}
}

type mermaidRenderArgs struct {
	Out      string
	Dark     bool
	Width    int
	Quality  string
	Deadline time.Duration
}

func parseMermaidRenderArgs(args []string) (mermaidRenderArgs, error) {
	out := mermaidRenderArgs{Quality: "terminal", Deadline: mermaidRenderTimeout, Width: 80}
	for i := 0; i < len(args); i++ {
		flag := args[i]
		need := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", flag)
			}
			i++
			return args[i], nil
		}
		switch flag {
		case "--out":
			v, err := need()
			if err != nil {
				return out, err
			}
			out.Out = v
		case "--theme":
			v, err := need()
			if err != nil {
				return out, err
			}
			switch v {
			case "dark":
				out.Dark = true
			case "light":
				out.Dark = false
			default:
				return out, fmt.Errorf("invalid --theme %q", v)
			}
		case "--width":
			v, err := need()
			if err != nil {
				return out, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return out, fmt.Errorf("invalid --width")
			}
			out.Width = n
		case "--max-height":
			if _, err := need(); err != nil {
				return out, err
			}
		case "--quality":
			v, err := need()
			if err != nil {
				return out, err
			}
			switch v {
			case "terminal", "t":
				out.Quality = "terminal"
			case "open", "o":
				out.Quality = "open"
			default:
				return out, fmt.Errorf("invalid --quality %q", v)
			}
		case "--deadline-ms":
			v, err := need()
			if err != nil {
				return out, err
			}
			ms, err := strconv.Atoi(v)
			if err != nil || ms < 0 {
				return out, fmt.Errorf("invalid --deadline-ms")
			}
			out.Deadline = time.Duration(ms) * time.Millisecond
		default:
			return out, fmt.Errorf("unknown flag %s", flag)
		}
	}
	if out.Out == "" {
		return out, fmt.Errorf("missing --out")
	}
	return out, nil
}

// TryMermaidRender intercepts argv before Cobra/TUI init. The caller should
// treat a true return as "already handled" (this helper exits the process).
func TryMermaidRender(args []string) bool {
	if len(args) < 2 || args[1] != mermaidRenderSubcommand {
		return false
	}
	if err := runMermaidRenderChild(args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
	return true
}

func runMermaidRenderChild(args []string) error {
	cfg, err := parseMermaidRenderArgs(args)
	if err != nil {
		return err
	}
	src, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Out), 0o700); err != nil {
		return err
	}
	art := mermaidArtForPNG(string(src), max(40, cfg.Width/8))
	if strings.TrimSpace(art) == "" {
		art = strings.TrimSpace(string(src))
	}
	return writeMermaidPNG(art, cfg.Out, cfg.Dark, cfg.Quality)
}

func renderMermaidViaSubprocess(src, out string, dark bool, widthPx int, openTier bool, timeout time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return err
	}
	quality := "terminal"
	if openTier {
		quality = "open"
	}
	theme := "light"
	if dark {
		theme = "dark"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, mermaidRenderSubcommand,
		"--out", out,
		"--theme", theme,
		"--quality", quality,
		"--width", strconv.Itoa(widthPx),
		"--deadline-ms", strconv.Itoa(int(timeout.Milliseconds())),
	)
	cmd.Stdin = bytes.NewReader([]byte(src))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return err
	}
	return mermaidPNGDecodable(out)
}

func mermaidPNGDecodable(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(b) < 8 || !bytes.Equal(b[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		return fmt.Errorf("not a png")
	}
	return nil
}

func (m *model) mermaidSessionDir() string {
	if m.sess != nil && m.sess.Dir != "" {
		return m.sess.Dir
	}
	return ""
}

func (m *model) mermaidThemeDark() bool {
	th := strings.ToLower(m.theme)
	return !strings.Contains(th, "day") && !strings.Contains(th, "light")
}

func (m *model) mermaidPendingFor(src string) bool {
	if m.mermaidBusy == nil {
		return false
	}
	return m.mermaidBusy[hashMermaidSource(src)]
}

func (m *model) clickMermaid(src string, kind mermaidClickKind) tea.Cmd {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil
	}
	if kind == mermaidClickCopySource {
		return m.copyText(src, false)
	}
	hash := hashMermaidSource(src)
	openTier := kind == mermaidClickOpen || kind == mermaidClickCopyPath
	name := mermaidCacheName(src, m.mermaidThemeDark(), m.transcriptWidth(), openTier)
	out := filepath.Join(mermaidCacheDir(m.mermaidSessionDir()), name)
	if mermaidPNGDecodable(out) == nil {
		return m.finishMermaidAction(out, kind)
	}
	if m.mermaidBusy == nil {
		m.mermaidBusy = map[string]bool{}
	}
	m.mermaidBusy[hash] = true
	return func() tea.Msg {
		err := renderMermaidViaSubprocess(src, out, m.mermaidThemeDark(), 2560, openTier, mermaidRenderTimeout)
		if err != nil {
			art := mermaidArtForPNG(src, max(40, m.transcriptWidth()))
			err = writeMermaidPNG(art, out, m.mermaidThemeDark(), "open")
		}
		return mermaidDoneMsg{hash: hash, path: out, err: err, action: kind}
	}
}

func (m *model) finishMermaidAction(path string, kind mermaidClickKind) tea.Cmd {
	switch kind {
	case mermaidClickCopyPath:
		return m.copyText(path, false)
	case mermaidClickOpen:
		if err := openMermaidPNG(path); err != nil {
			m.appendSys("Could not open diagram: " + err.Error())
			return nil
		}
		m.appendSys("Opened " + path)
		return nil
	}
	return nil
}

func (m *model) handleMermaidDone(msg mermaidDoneMsg) tea.Cmd {
	if m.mermaidBusy != nil {
		delete(m.mermaidBusy, msg.hash)
	}
	if msg.err != nil {
		m.appendSys("Diagram render failed")
		return nil
	}
	return m.finishMermaidAction(msg.path, msg.action)
}

func openMermaidPNG(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

func mermaidSourceFromBlock(bl block) string {
	switch bl.kind {
	case blockMermaid:
		return bl.body
	default:
		return firstMermaidFence(bl.body)
	}
}

func firstMermaidFence(src string) string {
	lines := strings.Split(src, "\n")
	var body []string
	in := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			info := strings.TrimSpace(strings.TrimPrefix(trim, "```"))
			if !in {
				tok, _, _ := strings.Cut(info, " ")
				if strings.EqualFold(tok, "mermaid") {
					in = true
					body = nil
					continue
				}
			} else {
				return strings.Join(body, "\n")
			}
		}
		if in {
			body = append(body, line)
		}
	}
	return ""
}

func hitMermaidButton(x int, btns []mermaidBtn) (mermaidClickKind, bool) {
	for _, b := range btns {
		if x >= b.Col && x < b.Col+b.Width {
			return b.Kind, true
		}
	}
	return 0, false
}
