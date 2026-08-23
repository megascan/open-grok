package mermaid

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Engine turns Mermaid source into a rasterized [Diagram]. Prefer
// [RenderChecked] over calling Render directly: it applies [Limits] and
// isolates panics. Implementations must be safe to share across goroutines.
type Engine interface {
	Render(source string, params Params) (Diagram, error)
}

// LayoutEngine turns Mermaid source into an SVG document. The default
// flowchart engine implements this so tests can inspect layout without a rasterizer.
type LayoutEngine interface {
	Layout(source string, params Params) (svg string, err error)
}

// FlowchartEngine is the default offline engine: simple flowchart/graph
// A-->B SVG layout, then an injected [Rasterizer]. Exotic diagrams return
// KindUnsupported. A nil Raster uses [StdlibRasterizer].
type FlowchartEngine struct {
	Raster Rasterizer
}

// NewFlowchartEngine returns a flowchart engine. A nil rasterizer selects
// [StdlibRasterizer] (no Chromium, no network).
func NewFlowchartEngine(r Rasterizer) *FlowchartEngine {
	if r == nil {
		r = StdlibRasterizer{}
	}
	return &FlowchartEngine{Raster: r}
}

// Render lays out source to SVG and rasterizes it.
func (e *FlowchartEngine) Render(source string, params Params) (Diagram, error) {
	if e == nil {
		return Diagram{}, &Error{Kind: KindUnsupported, Msg: "nil engine"}
	}
	svg, err := e.Layout(source, params)
	if err != nil {
		return Diagram{}, err
	}
	r := e.Raster
	if r == nil {
		r = StdlibRasterizer{}
	}
	d, err := r.Rasterize(svg, params)
	if err != nil {
		if _, ok := err.(*Error); ok {
			return Diagram{}, err
		}
		return Diagram{}, &Error{Kind: KindRasterize, Msg: err.Error()}
	}
	return d, nil
}

type node struct {
	id, label, shape string
}

type edge struct {
	from, to, label string
}

var edgeRe = regexp.MustCompile(`^([A-Za-z0-9_]+)(?:\[[^\]]*\]|\([^\)]*\)|\{[^}]*\}|\(\([^)]*\)\))?\s*(-->|---|-.->|==>)\s*(?:\|([^|]+)\|)?\s*([A-Za-z0-9_]+)(?:\[[^\]]*\]|\(([^\)]*)\)|\{([^}]*)\}|\(\(([^)]*)\)\))?`)

var supportedKinds = map[string]bool{
	"flowchart": true,
	"graph":     true,
}

var exoticKinds = []string{
	"sequencediagram", "pie", "classdiagram", "statediagram", "statediagram-v2",
	"gantt", "erdiagram", "journey", "gitgraph", "mindmap", "timeline",
	"sankey", "sankey-beta", "quadrantchart", "requirementdiagram",
	"c4context", "block-beta", "packet-beta", "architecture-beta",
	"xychart-beta", "kanban", "radar-beta",
}

// Layout produces an SVG for a simple flowchart/graph. It does not rasterize.
func (e *FlowchartEngine) Layout(source string, params Params) (string, error) {
	kind, dir, nodes, edges, extras, err := parseFlowchart(source)
	if err != nil {
		return "", err
	}
	if !supportedKinds[kind] {
		return "", &Error{Kind: KindUnsupported, Msg: "diagram type " + kind}
	}
	if len(extras) > 0 {
		return "", &Error{Kind: KindUnsupported, Msg: "exotic flowchart syntax: " + extras[0]}
	}
	if len(nodes) == 0 {
		return "", &Error{Kind: KindParse, Msg: "no nodes in flowchart"}
	}
	return layoutSVG(dir, nodes, edges, params.Dark), nil
}

func parseFlowchart(source string) (kind, dir string, nodes []node, edges []edge, extras []string, err error) {
	dir = "TD"
	kind = ""
	seen := map[string]int{}
	addNode := func(id, label, shape string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if label == "" {
			label = id
		}
		if shape == "" {
			shape = "rect"
		}
		if i, ok := seen[id]; ok {
			if label != id && nodes[i].label == nodes[i].id {
				nodes[i].label = label
			}
			if shape != "rect" && nodes[i].shape == "rect" {
				nodes[i].shape = shape
			}
			return
		}
		seen[id] = len(nodes)
		nodes = append(nodes, node{id: id, label: label, shape: shape})
	}

	lines := strings.Split(source, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		line = strings.TrimSuffix(line, ";")
		low := strings.ToLower(line)
		tok := ""
		if f := strings.Fields(low); len(f) > 0 {
			tok = strings.Trim(f[0], "{}")
		}
		switch {
		case tok == "flowchart":
			kind = "flowchart"
			fields := strings.Fields(line)
			if len(fields) > 1 {
				dir = strings.ToUpper(strings.Trim(fields[1], ";{}"))
			}
			continue
		case tok == "graph":
			kind = "graph"
			fields := strings.Fields(line)
			if len(fields) > 1 {
				dir = strings.ToUpper(strings.Trim(fields[1], ";{}"))
			}
			continue
		case strings.HasPrefix(low, "direction "):
			fields := strings.Fields(line)
			if len(fields) > 1 {
				dir = strings.ToUpper(fields[1])
			}
			continue
		}
		if kind == "" {
			if isExotic(tok) {
				return tok, dir, nil, nil, nil, &Error{Kind: KindUnsupported, Msg: "diagram type " + tok}
			}
			return tok, dir, nil, nil, nil, &Error{Kind: KindUnsupported, Msg: "diagram type " + firstNonEmpty(tok, "unknown")}
		}
		rest := line
		var prev string
		consumed := false
		for strings.TrimSpace(rest) != "" {
			probe := rest
			if prev != "" && arrowPrefix(strings.TrimSpace(rest)) {
				probe = prev + rest
			}
			m := edgeRe.FindStringSubmatch(strings.TrimSpace(probe))
			if m == nil {
				break
			}
			fromShape := shapeOf(m[1], probe)
			toShape := shapeOf(m[4], probe)
			addNode(m[1], firstNonEmpty(bracketLabel(probe, m[1]), m[1]), fromShape)
			addNode(m[4], firstNonEmpty(m[5], m[6], m[7], bracketLabelAfter(probe, m[4]), m[4]), toShape)
			edges = append(edges, edge{from: m[1], to: m[4], label: strings.TrimSpace(m[3])})
			consumed = true
			matched := m[0]
			// Advance past the match in `rest`, not the synthetic probe.
			trimRest := strings.TrimSpace(rest)
			if prev != "" && arrowPrefix(trimRest) {
				// probe was prev+rest; the match starts with prev.
				cut := strings.TrimSpace(probe[len(matched):])
				rest = cut
			} else {
				idx := strings.Index(rest, matched)
				if idx < 0 {
					rest = rest[len(rest):]
				} else {
					rest = rest[idx+len(matched):]
				}
			}
			prev = m[4]
		}
		if !consumed {
			if n := parseStandaloneNode(line); n.id != "" {
				addNode(n.id, n.label, n.shape)
				continue
			}
			extras = append(extras, line)
		}
	}
	if kind == "" {
		if strings.TrimSpace(source) == "" {
			return "", dir, nil, nil, nil, &Error{Kind: KindParse, Msg: "empty diagram"}
		}
		return "", dir, nil, nil, nil, &Error{Kind: KindParse, Msg: "no diagram type"}
	}
	return kind, dir, nodes, edges, extras, nil
}

func isExotic(tok string) bool {
	for _, k := range exoticKinds {
		if tok == k || strings.HasPrefix(tok, k) {
			return true
		}
	}
	return false
}

func arrowPrefix(s string) bool {
	return strings.HasPrefix(s, "-->") || strings.HasPrefix(s, "---") ||
		strings.HasPrefix(s, "-.->") || strings.HasPrefix(s, "==>")
}

func shapeOf(id, line string) string {
	n := parseStandaloneNode(id + afterID(line, id))
	if n.shape != "" {
		return n.shape
	}
	return "rect"
}

func afterID(line, id string) string {
	idx := strings.Index(line, id)
	if idx < 0 {
		return ""
	}
	return line[idx+len(id):]
}

func parseStandaloneNode(line string) node {
	line = strings.TrimSpace(line)
	for _, wrap := range []struct{ open, close, shape string }{
		{"((", "))", "circle"},
		{"[", "]", "rect"},
		{"{", "}", "diamond"},
		{"(", ")", "round"},
	} {
		open := strings.Index(line, wrap.open)
		if open <= 0 {
			continue
		}
		close := strings.LastIndex(line, wrap.close)
		if close <= open {
			continue
		}
		id := strings.TrimSpace(line[:open])
		if !isIdent(id) {
			continue
		}
		return node{id: id, label: strings.TrimSpace(line[open+len(wrap.open) : close]), shape: wrap.shape}
	}
	if isIdent(line) {
		return node{id: line, label: line, shape: "rect"}
	}
	return node{}
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

func bracketLabel(line, id string) string {
	idx := strings.Index(line, id)
	if idx < 0 {
		return ""
	}
	n := parseStandaloneNode(id + line[idx+len(id):])
	if n.label != "" {
		return n.label
	}
	return ""
}

func bracketLabelAfter(line, id string) string {
	idx := strings.LastIndex(line, id)
	if idx < 0 {
		return ""
	}
	n := parseStandaloneNode(line[idx:])
	if n.label != "" {
		return n.label
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

type box struct {
	x, y, w, h int
}

func layoutSVG(dir string, nodes []node, edges []edge, dark bool) string {
	bg, nodeFill, nodeStroke, fg, edgeCol := LightSurfaceHex, "#FFFFFF", "#2563EB", "#18181B", "#64748B"
	if dark {
		bg, nodeFill, nodeStroke, fg, edgeCol = DarkSurfaceHex, "#27272A", "#A1A1AA", "#FAFAFA", "#A1A1AA"
	}

	boxes := make([]box, len(nodes))
	const nodeH = 40
	for i, n := range nodes {
		w := 24 + 8*utf8.RuneCountInString(n.label)
		if w < 72 {
			w = 72
		}
		if w > 280 {
			w = 280
		}
		h := nodeH
		if n.shape == "diamond" {
			h = 56
			if w < 96 {
				w = 96
			}
		}
		if n.shape == "circle" {
			if w < h {
				w = h
			}
			h = w
		}
		boxes[i] = box{w: w, h: h}
	}

	layers := rankLayers(nodes, edges)
	dir = strings.ToUpper(dir)
	if dir == "RL" || dir == "BT" {
		for i, j := 0, len(layers)-1; i < j; i, j = i+1, j-1 {
			layers[i], layers[j] = layers[j], layers[i]
		}
	}
	lr := dir == "LR" || dir == "RL" || dir == ""
	if dir == "TD" || dir == "TB" || dir == "DT" || dir == "BT" {
		lr = false
	}

	const pad = 32
	const gapRank = 64
	const gapPeer = 24

	if lr {
		x := pad
		for _, layer := range layers {
			maxW := 0
			totalH := 0
			for i, idx := range layer {
				if boxes[idx].w > maxW {
					maxW = boxes[idx].w
				}
				totalH += boxes[idx].h
				if i > 0 {
					totalH += gapPeer
				}
			}
			y := pad
			for _, idx := range layer {
				boxes[idx].x = x
				boxes[idx].y = y
				y += boxes[idx].h + gapPeer
			}
			x += maxW + gapRank
		}
	} else {
		y := pad
		for _, layer := range layers {
			maxH := 0
			for _, idx := range layer {
				if boxes[idx].h > maxH {
					maxH = boxes[idx].h
				}
			}
			x := pad
			for _, idx := range layer {
				boxes[idx].x = x
				boxes[idx].y = y
				x += boxes[idx].w + gapPeer
			}
			y += maxH + gapRank
		}
	}

	maxX, maxY := pad, pad
	for _, b := range boxes {
		if b.x+b.w > maxX {
			maxX = b.x + b.w
		}
		if b.y+b.h > maxY {
			maxY = b.y + b.h
		}
	}
	W, H := maxX+pad, maxY+pad
	if W < 64 {
		W = 64
	}
	if H < 48 {
		H = 48
	}

	idx := map[string]int{}
	for i, n := range nodes {
		idx[n.id] = i
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`+"\n", W, H, W, H)
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%d" height="%d" fill="%s"/>`+"\n", W, H, bg)

	for _, e := range edges {
		ai, ok1 := idx[e.from]
		bi, ok2 := idx[e.to]
		if !ok1 || !ok2 {
			continue
		}
		x1, y1, x2, y2 := attach(boxes[ai], boxes[bi], lr)
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="2"/>`+"\n", x1, y1, x2, y2, edgeCol)
		ax, ay, bx, by, cx, cy := arrowHead(x1, y1, x2, y2, 10)
		fmt.Fprintf(&b, `<polygon points="%d,%d %d,%d %d,%d" fill="%s"/>`+"\n", ax, ay, bx, by, cx, cy, edgeCol)
		if e.label != "" {
			mx, my := (x1+x2)/2, (y1+y2)/2-6
			fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s" font-size="12" font-family="sans-serif" text-anchor="middle">%s</text>`+"\n", mx, my, fg, xmlEscape(e.label))
		}
	}

	for i, n := range nodes {
		bb := boxes[i]
		switch n.shape {
		case "diamond":
			pts := fmt.Sprintf("%d,%d %d,%d %d,%d %d,%d",
				bb.x+bb.w/2, bb.y, bb.x+bb.w, bb.y+bb.h/2, bb.x+bb.w/2, bb.y+bb.h, bb.x, bb.y+bb.h/2)
			fmt.Fprintf(&b, `<polygon points="%s" fill="%s" stroke="%s" stroke-width="2"/>`+"\n", pts, nodeFill, nodeStroke)
		case "circle":
			fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="%d" fill="%s" stroke="%s" stroke-width="2"/>`+"\n",
				bb.x+bb.w/2, bb.y+bb.h/2, min(bb.w, bb.h)/2-1, nodeFill, nodeStroke)
		case "round":
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="16" ry="16" fill="%s" stroke="%s" stroke-width="2"/>`+"\n",
				bb.x, bb.y, bb.w, bb.h, nodeFill, nodeStroke)
		default:
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="6" ry="6" fill="%s" stroke="%s" stroke-width="2"/>`+"\n",
				bb.x, bb.y, bb.w, bb.h, nodeFill, nodeStroke)
		}
		tx, ty := bb.x+bb.w/2, bb.y+bb.h/2
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s" font-size="14" font-family="sans-serif" text-anchor="middle" dominant-baseline="middle">%s</text>`+"\n",
			tx, ty, fg, xmlEscape(n.label))
	}
	b.WriteString("</svg>\n")
	return b.String()
}

func attach(a, b box, lr bool) (x1, y1, x2, y2 int) {
	acx, acy := a.x+a.w/2, a.y+a.h/2
	bcx, bcy := b.x+b.w/2, b.y+b.h/2
	if lr {
		if b.x >= a.x+a.w/2 {
			return a.x + a.w, acy, b.x, bcy
		}
		if a.x >= b.x+b.w/2 {
			return a.x, acy, b.x + b.w, bcy
		}
		return a.x + a.w, acy, b.x, bcy
	}
	if b.y >= a.y+a.h/2 {
		return acx, a.y + a.h, bcx, b.y
	}
	if a.y >= b.y+b.h/2 {
		return acx, a.y, bcx, b.y + b.h
	}
	return acx, a.y + a.h, bcx, b.y
}

func arrowHead(x1, y1, x2, y2, size int) (ax, ay, bx, by, cx, cy int) {
	dx := float64(x2 - x1)
	dy := float64(y2 - y1)
	dist := math.Hypot(dx, dy)
	if dist < 1 {
		return x2, y2, x2 - size, y2 - size/2, x2 - size, y2 + size/2
	}
	ux, uy := dx/dist, dy/dist
	px, py := -uy, ux
	ax, ay = x2, y2
	bx = x2 - int(ux*float64(size)+px*float64(size)/2)
	by = y2 - int(uy*float64(size)+py*float64(size)/2)
	cx = x2 - int(ux*float64(size)-px*float64(size)/2)
	cy = y2 - int(uy*float64(size)-py*float64(size)/2)
	return
}

func rankLayers(nodes []node, edges []edge) [][]int {
	n := len(nodes)
	index := map[string]int{}
	for i, nd := range nodes {
		index[nd.id] = i
	}
	indeg := make([]int, n)
	succ := make([][]int, n)
	pred := make([][]int, n)
	for _, e := range edges {
		a, ok1 := index[e.from]
		b, ok2 := index[e.to]
		if !ok1 || !ok2 || a == b {
			continue
		}
		succ[a] = append(succ[a], b)
		pred[b] = append(pred[b], a)
		indeg[b]++
	}
	var q []int
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			q = append(q, i)
		}
	}
	var order []int
	seen := make([]bool, n)
	for len(q) > 0 {
		i := q[0]
		q = q[1:]
		if seen[i] {
			continue
		}
		seen[i] = true
		order = append(order, i)
		for _, j := range succ[i] {
			indeg[j]--
			if indeg[j] == 0 {
				q = append(q, j)
			}
		}
	}
	for i := 0; i < n; i++ {
		if !seen[i] {
			order = append(order, i)
		}
	}
	rank := make([]int, n)
	for _, i := range order {
		m := 0
		for _, p := range pred[i] {
			if rank[p]+1 > m {
				m = rank[p] + 1
			}
		}
		rank[i] = m
	}
	maxR := 0
	for _, r := range rank {
		if r > maxR {
			maxR = r
		}
	}
	out := make([][]int, maxR+1)
	for _, i := range order {
		out[rank[i]] = append(out[rank[i]], i)
	}
	return out
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}
