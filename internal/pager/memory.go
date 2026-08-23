package pager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/megascan/open-grok/internal/config"
	"github.com/zeebo/blake3"
)

const (
	memorySplitMinWidth = 80
	memoryPreviewCap    = 1 << 20
)

type memoryMode int

const (
	memoryBrowse memoryMode = iota
	memoryFilter
	memoryConfirmDelete
)

// memoryEntry is one row in the /memory browser (header or file).
type memoryEntry struct {
	Path   string
	Source string // global, workspace, session
	Label  string
	Meta   string
	Header bool
}

type memoryModal struct {
	entries       []memoryEntry
	selected      int
	scroll        int
	preview       string
	previewScroll int
	mode          memoryMode
	query         string
	enabled       bool
	fullscreen    bool
	confirmIdx    int
}

func memoryFileLabel(path string) string {
	base := filepath.Base(path)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return path
	}
	return base
}

func formatMemoryBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func formatMemoryModified(mod time.Time, now time.Time) string {
	if mod.IsZero() {
		return "unknown"
	}
	if now.Before(mod) || now.Equal(mod) {
		return "just now"
	}
	delta := now.Sub(mod)
	switch {
	case delta < 60*time.Second:
		return "just now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(delta.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(delta.Hours()/24))
	}
}

func slugifyMemory(input string, maxLen int) string {
	var b strings.Builder
	prevDash := false
	for _, c := range strings.ToLower(input) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	runes := []rune(out)
	if len(runes) > maxLen {
		out = strings.Trim(string(runes[:maxLen]), "-")
	}
	return out
}

func normalizeGitRemote(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	var path string
	if i := strings.IndexByte(url, ':'); i >= 0 && strings.Contains(url[:i], "@") && !strings.Contains(url[:i], "/") {
		path = url[i+1:]
	} else if _, rest, ok := strings.Cut(url, "//"); ok {
		_, p, ok := strings.Cut(rest, "/")
		if !ok {
			return ""
		}
		path = p
	} else {
		return ""
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimSuffix(path, "/")
	return path
}

// workspaceMemoryHash matches Rust `{slug}-{hash8}` under ~/.entropy/memory/.
func workspaceMemoryHash(cwd string) string {
	identity := ""
	if out, err := exec.Command("git", "-C", cwd, "remote", "get-url", "origin").Output(); err == nil {
		identity = normalizeGitRemote(string(out))
	}
	slugSrc := cwd
	hashInput := cwd
	if identity != "" {
		hashInput = identity
		if i := strings.LastIndexByte(identity, '/'); i >= 0 {
			slugSrc = identity[i+1:]
		} else {
			slugSrc = identity
		}
	} else if abs, err := filepath.Abs(cwd); err == nil {
		hashInput = abs
		slugSrc = filepath.Base(abs)
	}
	slug := slugifyMemory(slugSrc, 40)
	if slug == "" {
		slug = "workspace"
	}
	sum := blake3.Sum256([]byte(hashInput))
	return fmt.Sprintf("%s-%x", slug, sum[:4])
}

type memoryFileInfo struct {
	Path     string
	Source   string
	Size     int64
	Modified time.Time
}

func listMemoryFiles(cwd string) []memoryFileInfo {
	root := filepath.Join(config.Home(), "memory")
	wsDir := filepath.Join(root, workspaceMemoryHash(cwd))
	var files []memoryFileInfo
	add := func(path, source string) {
		st, err := os.Stat(path)
		if err != nil || st.IsDir() {
			return
		}
		files = append(files, memoryFileInfo{
			Path: path, Source: source, Size: st.Size(), Modified: st.ModTime(),
		})
	}
	add(filepath.Join(root, "MEMORY.md"), "global")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "MEMORY.md" || !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		add(filepath.Join(root, name), "global")
	}
	add(filepath.Join(wsDir, "MEMORY.md"), "workspace")
	sessDir := filepath.Join(wsDir, "sessions")
	sess, _ := os.ReadDir(sessDir)
	var sessionPaths []string
	for _, e := range sess {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		sessionPaths = append(sessionPaths, filepath.Join(sessDir, e.Name()))
	}
	for _, p := range sessionPaths {
		add(p, "session")
	}
	return files
}

func buildMemoryEntries(files []memoryFileInfo, now time.Time) []memoryEntry {
	var global, workspace, session []memoryEntry
	for _, f := range files {
		row := memoryEntry{
			Path:   f.Path,
			Source: f.Source,
			Label:  memoryFileLabel(f.Path),
			Meta:   formatMemoryBytes(f.Size) + " · " + formatMemoryModified(f.Modified, now),
		}
		switch f.Source {
		case "global":
			global = append(global, row)
		case "workspace":
			workspace = append(workspace, row)
		default:
			session = append(session, row)
		}
	}
	for i, j := 0, len(session)-1; i < j; i, j = i+1, j-1 {
		session[i], session[j] = session[j], session[i]
	}
	var out []memoryEntry
	push := func(label string, items []memoryEntry) {
		if len(items) == 0 {
			return
		}
		out = append(out, memoryEntry{Label: label, Header: true})
		out = append(out, items...)
	}
	push("Global", global)
	push("Workspace", workspace)
	push("Sessions", session)
	return out
}

func computeMemoryFiltered(entries []memoryEntry, query string) []int {
	if query == "" {
		idx := make([]int, len(entries))
		for i := range entries {
			idx[i] = i
		}
		return idx
	}
	needle := strings.ToLower(query)
	var result []int
	pending := -1
	for i, e := range entries {
		if e.Header {
			pending = i
			continue
		}
		if strings.Contains(strings.ToLower(e.Label), needle) || strings.Contains(strings.ToLower(e.Source), needle) {
			if pending >= 0 {
				result = append(result, pending)
				pending = -1
			}
			result = append(result, i)
		}
	}
	return result
}

func newMemoryModal(cwd string, enabled bool, query string, fullscreen bool) *memoryModal {
	m := &memoryModal{
		entries:    buildMemoryEntries(listMemoryFiles(cwd), time.Now()),
		enabled:    enabled,
		query:      query,
		fullscreen: fullscreen,
		mode:       memoryBrowse,
	}
	if query != "" {
		m.mode = memoryFilter
	}
	m.advancePastHeaders()
	m.loadPreview()
	return m
}

func (s *memoryModal) filtered() []int {
	return computeMemoryFiltered(s.entries, s.query)
}

func (s *memoryModal) selectedEntry() *memoryEntry {
	f := s.filtered()
	if s.selected < 0 || s.selected >= len(f) {
		return nil
	}
	e := &s.entries[f[s.selected]]
	return e
}

func (s *memoryModal) advancePastHeaders() {
	f := s.filtered()
	for i, orig := range f {
		if !s.entries[orig].Header {
			s.selected = i
			return
		}
	}
	s.selected = 0
}

func (s *memoryModal) advance(delta int) bool {
	f := s.filtered()
	if len(f) == 0 {
		return false
	}
	next := s.selected + delta
	for next >= 0 && next < len(f) {
		if !s.entries[f[next]].Header {
			s.selected = next
			return true
		}
		next += delta
	}
	return false
}

func (s *memoryModal) selectNext() {
	if s.advance(1) {
		s.loadPreview()
	}
}

func (s *memoryModal) selectPrev() {
	if s.advance(-1) {
		s.loadPreview()
	}
}

func (s *memoryModal) clampSelected() {
	f := s.filtered()
	if len(f) == 0 {
		s.selected = 0
		s.preview = ""
		return
	}
	if s.selected >= len(f) {
		s.selected = len(f) - 1
	}
	if s.entries[f[s.selected]].Header {
		s.advancePastHeaders()
	}
	s.loadPreview()
}

func (s *memoryModal) loadPreview() {
	s.previewScroll = 0
	e := s.selectedEntry()
	if e == nil || e.Header || e.Path == "" {
		s.preview = ""
		return
	}
	st, err := os.Stat(e.Path)
	if err != nil {
		s.preview = ""
		return
	}
	if st.Size() > memoryPreviewCap {
		s.preview = "*(File too large to preview)*"
		return
	}
	data, err := os.ReadFile(e.Path)
	if err != nil {
		s.preview = ""
		return
	}
	s.preview = string(data)
}

func (m *model) openMemory(query string) {
	fs := false
	if m.cfg.Prefs != nil {
		fs = strings.EqualFold(m.cfg.Prefs["memory_modal_fullscreen"], "true")
	}
	m.mem = newMemoryModal(m.opts.CWD, m.cfg.MemoryEnabled(), query, fs)
	m.overlay = overlayMemory
}

func (m *model) persistMemoryFullscreen(on bool) {
	if m.cfg.Prefs == nil {
		m.cfg.Prefs = config.Prefs{}
	}
	if on {
		m.cfg.Prefs["memory_modal_fullscreen"] = "true"
	} else {
		delete(m.cfg.Prefs, "memory_modal_fullscreen")
	}
	_ = config.Save(m.cfg)
}

func (m *model) renderMemory(termW int) string {
	s := m.mem
	if s == nil {
		return ""
	}
	w := termW
	if s.fullscreen {
		w = max(44, m.width-2)
	} else {
		w = min(140, max(44, m.width*3/4))
	}
	showPreview := w >= memorySplitMinWidth
	f := s.filtered()
	listW := w
	if showPreview {
		listW = int(float64(w) * 0.40)
	}
	var list strings.Builder
	placeholder := "/ to filter..."
	if s.mode == memoryFilter {
		placeholder = "type to filter..."
	}
	if s.query == "" {
		list.WriteString(dimStyle.Render(placeholder) + "\n")
	} else {
		q := s.query
		if s.mode == memoryFilter {
			q += "█"
		}
		list.WriteString(q + "\n")
	}
	avail := max(6, min(18, m.height-10))
	if s.selected < s.scroll {
		s.scroll = s.selected
	}
	if s.selected >= s.scroll+avail {
		s.scroll = s.selected - avail + 1
	}
	end := min(len(f), s.scroll+avail)
	for i := s.scroll; i < end; i++ {
		e := s.entries[f[i]]
		if e.Header {
			list.WriteString(titleStyle.Render(e.Label) + "\n")
			continue
		}
		line := e.Label
		meta := e.Meta
		if s.mode == memoryConfirmDelete && s.confirmIdx == i {
			meta = "[x to confirm]"
			line = errStyle.Render(line + "  " + meta)
		} else if i == s.selected {
			line = selStyle.Render(line + "  " + meta)
		} else {
			line = " " + line + "  " + dimStyle.Render(meta)
		}
		list.WriteString(line + "\n")
	}
	if len(f) == 0 {
		list.WriteString(dimStyle.Render("(empty)") + "\n")
	}
	listBody := list.String()
	var body string
	if showPreview {
		previewW := w - listW - 1
		prev := s.preview
		if prev == "" {
			prev = "No file selected"
		}
		plines := strings.Split(prev, "\n")
		if s.previewScroll >= len(plines) {
			s.previewScroll = 0
		}
		maxP := avail + 1
		if s.previewScroll+maxP > len(plines) && len(plines) > maxP {
			s.previewScroll = len(plines) - maxP
		}
		from := s.previewScroll
		to := min(len(plines), from+maxP)
		ptext := strings.Join(plines[from:to], "\n")
		left := lipgloss.NewStyle().Width(listW).Render(listBody)
		right := lipgloss.NewStyle().Width(max(10, previewW)).Render(ptext)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, dimStyle.Render("│")+" "+right)
	} else {
		body = listBody
	}
	toggle := "t toggle (off)"
	if s.enabled {
		toggle = "t toggle (on)"
	}
	fs := "^F fullscreen"
	if s.fullscreen {
		fs = "^F windowed"
	}
	footer := dimStyle.Render("↑/↓ nav  ·  / search  ·  x delete  ·  y copy path  ·  " + toggle + "  ·  " + fs + "  ·  esc")
	switch s.mode {
	case memoryFilter:
		footer = dimStyle.Render("type to filter  ·  ↑/↓ nav  ·  esc exit filter")
	case memoryConfirmDelete:
		footer = dimStyle.Render("x confirm delete  ·  any other key cancel")
	}
	title := titleStyle.Render("Memory")
	if !s.enabled {
		title += dimStyle.Render("  (off)")
	}
	return modalBox.Width(w).Render(title + "\n" + body + "\n" + footer)
}

func (m *model) handleMemoryKey(msg tea.KeyMsg) tea.Cmd {
	s := m.mem
	if s == nil {
		m.overlay = overlayNone
		return nil
	}
	key := msg.String()
	if s.mode == memoryConfirmDelete {
		if key == "x" {
			f := s.filtered()
			if s.confirmIdx >= 0 && s.confirmIdx < len(f) {
				orig := f[s.confirmIdx]
				path := s.entries[orig].Path
				s.entries = append(s.entries[:orig], s.entries[orig+1:]...)
				s.mode = memoryBrowse
				s.clampSelected()
				_ = os.Remove(path)
			}
			return nil
		}
		s.mode = memoryBrowse
		return nil
	}
	if s.mode == memoryFilter {
		switch key {
		case "esc":
			s.mode = memoryBrowse
		case "down":
			s.selectNext()
		case "up":
			s.selectPrev()
		case "backspace":
			if s.query != "" {
				s.query = trimLastRune(s.query)
				s.clampSelected()
			}
		default:
			if msg.Type == tea.KeyRunes {
				s.query += string(msg.Runes)
				s.clampSelected()
			}
		}
		return nil
	}
	if key == "ctrl+f" {
		s.fullscreen = !s.fullscreen
		m.persistMemoryFullscreen(s.fullscreen)
		return nil
	}
	switch key {
	case "esc":
		m.overlay = overlayNone
		m.mem = nil
	case "down", "j":
		s.selectNext()
	case "up", "k":
		s.selectPrev()
	case "pgdown":
		for i := 0; i < 10; i++ {
			s.advance(1)
		}
		s.loadPreview()
	case "pgup":
		for i := 0; i < 10; i++ {
			s.advance(-1)
		}
		s.loadPreview()
	case "x":
		if e := s.selectedEntry(); e != nil && !e.Header && e.Source == "session" {
			s.mode = memoryConfirmDelete
			s.confirmIdx = s.selected
		}
	case "y":
		if e := s.selectedEntry(); e != nil && !e.Header {
			return m.copyText(e.Path, false)
		}
	case "t":
		s.enabled = !s.enabled
		m.cfg.Features.Memory = s.enabled
		_ = config.Save(m.cfg)
	case "/", "i":
		s.mode = memoryFilter
	case "backspace":
		if s.query != "" {
			s.query = trimLastRune(s.query)
			s.clampSelected()
		}
	}
	return nil
}

func trimLastRune(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return string(r[:len(r)-1])
}
