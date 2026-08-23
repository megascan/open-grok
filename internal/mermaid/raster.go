package mermaid

import (
	"bytes"
	"encoding/xml"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxOutputDimension = 16384

// Rasterizer turns an SVG string into PNG bytes. Implementations must not
// fetch remote resources (no http/file resolvers, no Chromium).
type Rasterizer interface {
	Rasterize(svg string, params Params) (Diagram, error)
}

// StdlibRasterizer paints a conservative SVG subset with image/png.
//
// Supported: svg, g, rect, line, polygon, polyline, circle, ellipse, text.
// <image>, <use>, <script>, <foreignObject>, and url() paints are ignored
// (never resolved). No network, no fontconfig, no Chromium.
type StdlibRasterizer struct{}

// Rasterize converts svg to PNG. Output size follows Params.TargetWidthPx
// (and MaxHeightPx / the 32 MP area cap), matching xai-grok-mermaid::rasterize.
func (StdlibRasterizer) Rasterize(svg string, params Params) (Diagram, error) {
	params = params.withDefaults()
	doc, err := parseSVG(svg)
	if err != nil {
		return Diagram{}, &Error{Kind: KindRasterize, Msg: err.Error()}
	}
	if doc.w <= 0 || doc.h <= 0 {
		return Diagram{}, &Error{Kind: KindRasterize, Msg: "diagram has zero size"}
	}
	outW, outH := clampDimensions(doc.w, doc.h, params)
	img := image.NewRGBA(image.Rect(0, 0, outW, outH))
	bg := parseHex(LightSurfaceHex)
	if params.Dark {
		bg = parseHex(DarkSurfaceHex)
	}
	fillRect(img, 0, 0, outW, outH, bg)
	sx := float64(outW) / doc.w
	sy := float64(outH) / doc.h
	for _, p := range doc.prims {
		p.draw(img, sx, sy)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return Diagram{}, &Error{Kind: KindRasterize, Msg: err.Error()}
	}
	return Diagram{PNG: buf.Bytes(), WidthPx: outW, HeightPx: outH}, nil
}

func clampDimensions(baseW, baseH float64, params Params) (int, int) {
	scale := float64(params.TargetWidthPx) / baseW
	if !isFinite(scale) || scale <= 0 {
		scale = 1
	}
	if params.MaxHeightPx > 0 && baseH*scale > float64(params.MaxHeightPx) {
		scale = float64(params.MaxHeightPx) / baseH
	}
	maxArea := MAX_OUTPUT_MEGAPIXELS * 1_000_000.0
	area := (baseW * scale) * (baseH * scale)
	if area > maxArea {
		scale *= math.Sqrt(maxArea / area)
	}
	if scale < math.SmallestNonzeroFloat64 {
		scale = math.SmallestNonzeroFloat64
	}
	w := int(math.Floor(baseW * scale))
	h := int(math.Floor(baseH * scale))
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	if w > maxOutputDimension {
		w = maxOutputDimension
	}
	if h > maxOutputDimension {
		h = maxOutputDimension
	}
	maxA := uint64(MAX_OUTPUT_MEGAPIXELS * 1_000_000.0)
	if uint64(w)*uint64(h) > maxA {
		if w >= h {
			w = int(maxA / uint64(h))
			if w < 1 {
				w = 1
			}
		} else {
			h = int(maxA / uint64(w))
			if h < 1 {
				h = 1
			}
		}
	}
	return w, h
}

func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

type svgDoc struct {
	w, h  float64
	prims []prim
}

type prim interface {
	draw(img *image.RGBA, sx, sy float64)
}

type rectP struct {
	x, y, w, h, rx float64
	fill           color.RGBA
	stroke         color.RGBA
	strokeW        float64
	hasFill        bool
	hasStroke      bool
}

func (p rectP) draw(img *image.RGBA, sx, sy float64) {
	x := int(math.Round(p.x * sx))
	y := int(math.Round(p.y * sy))
	w := int(math.Round(p.w * sx))
	h := int(math.Round(p.h * sy))
	if p.hasFill {
		fillRect(img, x, y, w, h, p.fill)
	}
	if p.hasStroke {
		sw := int(math.Max(1, math.Round(p.strokeW*math.Min(sx, sy))))
		strokeRect(img, x, y, w, h, sw, p.stroke)
	}
}

type lineP struct {
	x1, y1, x2, y2 float64
	stroke         color.RGBA
	strokeW        float64
}

func (p lineP) draw(img *image.RGBA, sx, sy float64) {
	x1 := int(math.Round(p.x1 * sx))
	y1 := int(math.Round(p.y1 * sy))
	x2 := int(math.Round(p.x2 * sx))
	y2 := int(math.Round(p.y2 * sy))
	sw := int(math.Max(1, math.Round(p.strokeW*math.Min(sx, sy))))
	strokeLine(img, x1, y1, x2, y2, sw, p.stroke)
}

type polyP struct {
	pts       [][2]float64
	fill      color.RGBA
	stroke    color.RGBA
	strokeW   float64
	hasFill   bool
	hasStroke bool
	closed    bool
}

func (p polyP) draw(img *image.RGBA, sx, sy float64) {
	pts := make([][2]int, len(p.pts))
	for i, pt := range p.pts {
		pts[i] = [2]int{int(math.Round(pt[0] * sx)), int(math.Round(pt[1] * sy))}
	}
	if p.hasFill && p.closed && len(pts) >= 3 {
		fillPolygon(img, pts, p.fill)
	}
	if p.hasStroke && len(pts) >= 2 {
		sw := int(math.Max(1, math.Round(p.strokeW*math.Min(sx, sy))))
		n := len(pts)
		lim := n - 1
		if p.closed {
			lim = n
		}
		for i := 0; i < lim; i++ {
			j := (i + 1) % n
			strokeLine(img, pts[i][0], pts[i][1], pts[j][0], pts[j][1], sw, p.stroke)
		}
	}
}

type ellipseP struct {
	cx, cy, rx, ry float64
	fill           color.RGBA
	stroke         color.RGBA
	strokeW        float64
	hasFill        bool
	hasStroke      bool
}

func (p ellipseP) draw(img *image.RGBA, sx, sy float64) {
	cx := p.cx * sx
	cy := p.cy * sy
	rx := p.rx * sx
	ry := p.ry * sy
	if p.hasFill {
		fillEllipse(img, cx, cy, rx, ry, p.fill)
	}
	if p.hasStroke {
		sw := math.Max(1, p.strokeW*math.Min(sx, sy))
		strokeEllipse(img, cx, cy, rx, ry, sw, p.stroke)
	}
}

type textP struct {
	x, y     float64
	s        string
	fill     color.RGBA
	size     float64
	anchor   string
	baseline string
}

func (p textP) draw(img *image.RGBA, sx, sy float64) {
	scale := math.Min(sx, sy)
	size := p.size * scale
	if size < 8 {
		size = 8
	}
	x := p.x * sx
	y := p.y * sy
	gh := size
	gw := size * 6 / 7
	width := float64(utf8.RuneCountInString(p.s)) * gw
	switch p.anchor {
	case "middle":
		x -= width / 2
	case "end":
		x -= width
	}
	switch p.baseline {
	case "middle", "central":
		y -= gh / 2
	default:
		y -= gh * 0.8
	}
	drawText(img, int(math.Round(x)), int(math.Round(y)), p.s, p.fill, int(math.Round(gh)))
}

var forbiddenSVG = map[string]bool{
	"image":            true,
	"use":              true,
	"script":           true,
	"foreignobject":    true,
	"iframe":           true,
	"link":             true,
	"style":            true,
	"video":            true,
	"audio":            true,
	"feimage":          true,
	"animate":          true,
	"animatetransform": true,
}

func parseSVG(s string) (*svgDoc, error) {
	dec := xml.NewDecoder(strings.NewReader(s))
	// Non-UTF-8 encodings error locally; CharsetReader stays nil so we never
	// fetch a charset mapping over the network.
	doc := &svgDoc{}
	var stack []map[string]string // inherited presentation attrs (fill/stroke)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if forbiddenSVG[name] || hasRemoteRef(t.Attr) {
				if err := dec.Skip(); err != nil && err != io.EOF {
					return nil, err
				}
				continue
			}
			attrs := attrMap(t.Attr)
			parent := map[string]string{}
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			merged := inherit(parent, attrs)
			switch name {
			case "svg":
				doc.w, doc.h = svgSize(attrs)
			case "rect":
				doc.prims = append(doc.prims, parseRect(merged))
			case "line":
				doc.prims = append(doc.prims, parseLine(merged))
			case "polygon", "polyline":
				doc.prims = append(doc.prims, parsePoly(merged, name == "polygon"))
			case "circle":
				doc.prims = append(doc.prims, parseCircle(merged))
			case "ellipse":
				doc.prims = append(doc.prims, parseEllipse(merged))
			case "text":
				var inner string
				if err := dec.DecodeElement(&inner, &t); err != nil {
					return nil, err
				}
				doc.prims = append(doc.prims, parseText(merged, inner))
				continue // DecodeElement consumes the matching EndElement.
			}
			stack = append(stack, merged)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if doc.w <= 0 || doc.h <= 0 {
		return nil, fmtRaster("missing svg width/height")
	}
	return doc, nil
}

func fmtRaster(msg string) error {
	return &Error{Kind: KindRasterize, Msg: msg}
}

func hasRemoteRef(attrs []xml.Attr) bool {
	for _, a := range attrs {
		n := strings.ToLower(a.Name.Local)
		v := strings.TrimSpace(a.Value)
		low := strings.ToLower(v)
		if n == "href" || n == "xlink:href" || a.Name.Space == "http://www.w3.org/1999/xlink" {
			if strings.HasPrefix(low, "http:") || strings.HasPrefix(low, "https:") ||
				strings.HasPrefix(low, "file:") || strings.HasPrefix(low, "//") {
				return true
			}
		}
		if strings.Contains(low, "url(http") || strings.Contains(low, "url('http") ||
			strings.Contains(low, `url("http`) {
			return true
		}
	}
	return false
}

func attrMap(attrs []xml.Attr) map[string]string {
	m := map[string]string{}
	for _, a := range attrs {
		m[strings.ToLower(a.Name.Local)] = a.Value
	}
	return m
}

func inherit(parent, own map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range parent {
		if k == "fill" || k == "stroke" || k == "stroke-width" || k == "font-size" || k == "text-anchor" || k == "dominant-baseline" {
			out[k] = v
		}
	}
	for k, v := range own {
		out[k] = v
	}
	return out
}

func svgSize(attrs map[string]string) (w, h float64) {
	w = parseLen(attrs["width"])
	h = parseLen(attrs["height"])
	if (w <= 0 || h <= 0) && attrs["viewbox"] != "" {
		parts := strings.FieldsFunc(attrs["viewbox"], func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		})
		if len(parts) == 4 {
			if w <= 0 {
				w = parseLen(parts[2])
			}
			if h <= 0 {
				h = parseLen(parts[3])
			}
		}
	}
	return w, h
}

func parseLen(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(s, "px"), "pt"))
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || !isFinite(f) {
		return 0
	}
	return f
}

func parseRect(a map[string]string) rectP {
	p := rectP{
		x: parseLen(a["x"]), y: parseLen(a["y"]),
		w: parseLen(a["width"]), h: parseLen(a["height"]),
		rx: parseLen(a["rx"]), strokeW: strokeWidth(a),
	}
	p.fill, p.hasFill = parseColor(a["fill"], true)
	p.stroke, p.hasStroke = parseColor(a["stroke"], false)
	return p
}

func parseLine(a map[string]string) lineP {
	p := lineP{
		x1: parseLen(a["x1"]), y1: parseLen(a["y1"]),
		x2: parseLen(a["x2"]), y2: parseLen(a["y2"]),
		strokeW: strokeWidth(a),
	}
	p.stroke, _ = parseColor(firstNonEmpty(a["stroke"], "#000000"), true)
	return p
}

func parsePoly(a map[string]string, closed bool) polyP {
	p := polyP{strokeW: strokeWidth(a), closed: closed, pts: parsePoints(a["points"])}
	p.fill, p.hasFill = parseColor(a["fill"], closed)
	p.stroke, p.hasStroke = parseColor(a["stroke"], !closed)
	if !closed && !p.hasStroke {
		p.stroke, p.hasStroke = parseColor("#000000", true)
	}
	return p
}

func parseCircle(a map[string]string) ellipseP {
	r := parseLen(a["r"])
	p := ellipseP{
		cx: parseLen(a["cx"]), cy: parseLen(a["cy"]), rx: r, ry: r,
		strokeW: strokeWidth(a),
	}
	p.fill, p.hasFill = parseColor(a["fill"], true)
	p.stroke, p.hasStroke = parseColor(a["stroke"], false)
	return p
}

func parseEllipse(a map[string]string) ellipseP {
	p := ellipseP{
		cx: parseLen(a["cx"]), cy: parseLen(a["cy"]),
		rx: parseLen(a["rx"]), ry: parseLen(a["ry"]),
		strokeW: strokeWidth(a),
	}
	p.fill, p.hasFill = parseColor(a["fill"], true)
	p.stroke, p.hasStroke = parseColor(a["stroke"], false)
	return p
}

func parseText(a map[string]string, inner string) textP {
	size := parseLen(a["font-size"])
	if size <= 0 {
		size = 14
	}
	c, ok := parseColor(a["fill"], true)
	if !ok {
		c = color.RGBA{0, 0, 0, 255}
	}
	return textP{
		x: parseLen(a["x"]), y: parseLen(a["y"]),
		s: strings.TrimSpace(inner), fill: c, size: size,
		anchor: strings.ToLower(a["text-anchor"]), baseline: strings.ToLower(a["dominant-baseline"]),
	}
}

func strokeWidth(a map[string]string) float64 {
	w := parseLen(a["stroke-width"])
	if w <= 0 {
		return 1
	}
	return w
}

func parsePoints(s string) [][2]float64 {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	var pts [][2]float64
	for i := 0; i+1 < len(fields); i += 2 {
		x, y := parseLen(fields[i]), parseLen(fields[i+1])
		pts = append(pts, [2]float64{x, y})
	}
	return pts
}

func parseColor(s string, defaultFill bool) (color.RGBA, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		if defaultFill {
			return color.RGBA{0, 0, 0, 255}, true
		}
		return color.RGBA{}, false
	}
	low := strings.ToLower(s)
	if low == "none" || low == "transparent" || strings.HasPrefix(low, "url(") {
		return color.RGBA{}, false
	}
	switch low {
	case "black":
		return color.RGBA{0, 0, 0, 255}, true
	case "white":
		return color.RGBA{255, 255, 255, 255}, true
	case "red":
		return color.RGBA{255, 0, 0, 255}, true
	case "green":
		return color.RGBA{0, 128, 0, 255}, true
	case "blue":
		return color.RGBA{0, 0, 255, 255}, true
	}
	if strings.HasPrefix(s, "#") {
		return parseHex(s), true
	}
	if strings.HasPrefix(low, "rgb(") {
		inner := strings.TrimSuffix(strings.TrimPrefix(low, "rgb("), ")")
		parts := strings.Split(inner, ",")
		if len(parts) == 3 {
			r, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
			g, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			b, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
			return color.RGBA{clampU8(r), clampU8(g), clampU8(b), 255}, true
		}
	}
	return color.RGBA{}, false
}

func parseHex(s string) color.RGBA {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	n := len(s)
	read := func(from, to int) uint8 {
		v, _ := strconv.ParseUint(s[from:to], 16, 8)
		return uint8(v)
	}
	switch n {
	case 3:
		r, _ := strconv.ParseUint(string(s[0])+string(s[0]), 16, 8)
		g, _ := strconv.ParseUint(string(s[1])+string(s[1]), 16, 8)
		b, _ := strconv.ParseUint(string(s[2])+string(s[2]), 16, 8)
		return color.RGBA{uint8(r), uint8(g), uint8(b), 255}
	case 6:
		return color.RGBA{read(0, 2), read(2, 4), read(4, 6), 255}
	case 8:
		return color.RGBA{read(0, 2), read(2, 4), read(4, 6), read(6, 8)}
	}
	return color.RGBA{0, 0, 0, 255}
}

func clampU8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	b := img.Bounds()
	for yy := y; yy < y+h; yy++ {
		if yy < b.Min.Y || yy >= b.Max.Y {
			continue
		}
		for xx := x; xx < x+w; xx++ {
			if xx < b.Min.X || xx >= b.Max.X {
				continue
			}
			img.SetRGBA(xx, yy, c)
		}
	}
}

func strokeRect(img *image.RGBA, x, y, w, h, sw int, c color.RGBA) {
	if sw < 1 {
		sw = 1
	}
	fillRect(img, x, y, w, sw, c)
	fillRect(img, x, y+h-sw, w, sw, c)
	fillRect(img, x, y, sw, h, c)
	fillRect(img, x+w-sw, y, sw, h, c)
}

func strokeLine(img *image.RGBA, x1, y1, x2, y2, w int, c color.RGBA) {
	if w < 1 {
		w = 1
	}
	dx := abs(x2 - x1)
	dy := abs(y2 - y1)
	sx, sy := 1, 1
	if x1 > x2 {
		sx = -1
	}
	if y1 > y2 {
		sy = -1
	}
	err := dx - dy
	x, y := x1, y1
	r := w / 2
	b := img.Bounds()
	for {
		for oy := -r; oy <= r; oy++ {
			for ox := -r; ox <= r; ox++ {
				xx, yy := x+ox, y+oy
				if xx >= b.Min.X && xx < b.Max.X && yy >= b.Min.Y && yy < b.Max.Y {
					img.SetRGBA(xx, yy, c)
				}
			}
		}
		if x == x2 && y == y2 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x += sx
		}
		if e2 < dx {
			err += dx
			y += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func fillPolygon(img *image.RGBA, pts [][2]int, c color.RGBA) {
	if len(pts) < 3 {
		return
	}
	minY, maxY := pts[0][1], pts[0][1]
	for _, p := range pts {
		if p[1] < minY {
			minY = p[1]
		}
		if p[1] > maxY {
			maxY = p[1]
		}
	}
	b := img.Bounds()
	if minY < b.Min.Y {
		minY = b.Min.Y
	}
	if maxY >= b.Max.Y {
		maxY = b.Max.Y - 1
	}
	n := len(pts)
	for y := minY; y <= maxY; y++ {
		var xs []int
		for i := 0; i < n; i++ {
			j := (i + 1) % n
			yi, yj := pts[i][1], pts[j][1]
			if yj == yi {
				continue
			}
			if (yi <= y && yj > y) || (yj <= y && yi > y) {
				xi, xj := pts[i][0], pts[j][0]
				x := xi + int(float64(y-yi)*float64(xj-xi)/float64(yj-yi))
				xs = append(xs, x)
			}
		}
		// insertion sort
		for i := 1; i < len(xs); i++ {
			v := xs[i]
			j := i
			for j > 0 && xs[j-1] > v {
				xs[j] = xs[j-1]
				j--
			}
			xs[j] = v
		}
		for i := 0; i+1 < len(xs); i += 2 {
			x1, x2 := xs[i], xs[i+1]
			if x1 > x2 {
				x1, x2 = x2, x1
			}
			for x := x1; x <= x2; x++ {
				if x >= b.Min.X && x < b.Max.X {
					img.SetRGBA(x, y, c)
				}
			}
		}
	}
}

func fillEllipse(img *image.RGBA, cx, cy, rx, ry float64, c color.RGBA) {
	if rx <= 0 || ry <= 0 {
		return
	}
	b := img.Bounds()
	minX := int(math.Floor(cx - rx))
	maxX := int(math.Ceil(cx + rx))
	minY := int(math.Floor(cy - ry))
	maxY := int(math.Ceil(cy + ry))
	rx2 := rx * rx
	ry2 := ry * ry
	for y := minY; y <= maxY; y++ {
		if y < b.Min.Y || y >= b.Max.Y {
			continue
		}
		dy := float64(y) + 0.5 - cy
		for x := minX; x <= maxX; x++ {
			if x < b.Min.X || x >= b.Max.X {
				continue
			}
			dx := float64(x) + 0.5 - cx
			if dx*dx/rx2+dy*dy/ry2 <= 1 {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func strokeEllipse(img *image.RGBA, cx, cy, rx, ry, sw float64, c color.RGBA) {
	if rx <= 0 || ry <= 0 {
		return
	}
	outerX, outerY := rx+sw/2, ry+sw/2
	innerX, innerY := rx-sw/2, ry-sw/2
	if innerX < 0 {
		innerX = 0
	}
	if innerY < 0 {
		innerY = 0
	}
	b := img.Bounds()
	minX := int(math.Floor(cx - outerX))
	maxX := int(math.Ceil(cx + outerX))
	minY := int(math.Floor(cy - outerY))
	maxY := int(math.Ceil(cy + outerY))
	for y := minY; y <= maxY; y++ {
		if y < b.Min.Y || y >= b.Max.Y {
			continue
		}
		dy := float64(y) + 0.5 - cy
		for x := minX; x <= maxX; x++ {
			if x < b.Min.X || x >= b.Max.X {
				continue
			}
			dx := float64(x) + 0.5 - cx
			on := dx*dx/(outerX*outerX)+dy*dy/(outerY*outerY) <= 1
			off := innerX > 0 && innerY > 0 && dx*dx/(innerX*innerX)+dy*dy/(innerY*innerY) <= 1
			if on && !off {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func drawText(img *image.RGBA, x, y int, s string, fg color.RGBA, size int) {
	if size < 7 {
		size = 7
	}
	gw, gh := 5, 7
	scale := size / gh
	if scale < 1 {
		scale = 1
	}
	col := 0
	for _, r := range s {
		bits := glyphBits(r)
		ox := x + col*(gw+1)*scale
		oy := y
		for gy := 0; gy < gh; gy++ {
			row := bits[gy]
			for gx := 0; gx < gw; gx++ {
				if row&(1<<uint(4-gx)) == 0 {
					continue
				}
				fillRect(img, ox+gx*scale, oy+gy*scale, scale, scale, fg)
			}
		}
		col++
	}
}

func glyphBits(r rune) [7]byte {
	if r >= 0x20 && r <= 0x7e {
		return asciiGlyphs[r-0x20]
	}
	return [7]byte{0x1F, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1F}
}

// 5x7 glyphs for ASCII 0x20–0x7E (MSB is leftmost pixel). Used only to paint
// SVG <text> labels, never to glyph-raster a whole ASCII-art diagram.
var asciiGlyphs = [95][7]byte{
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	{0x04, 0x04, 0x04, 0x04, 0x00, 0x04, 0x00},
	{0x0A, 0x0A, 0x00, 0x00, 0x00, 0x00, 0x00},
	{0x0A, 0x1F, 0x0A, 0x1F, 0x0A, 0x00, 0x00},
	{0x04, 0x0F, 0x14, 0x0E, 0x05, 0x1E, 0x04},
	{0x18, 0x19, 0x02, 0x04, 0x08, 0x13, 0x03},
	{0x08, 0x14, 0x08, 0x15, 0x12, 0x0D, 0x00},
	{0x06, 0x04, 0x08, 0x00, 0x00, 0x00, 0x00},
	{0x02, 0x04, 0x08, 0x08, 0x08, 0x04, 0x02},
	{0x08, 0x04, 0x02, 0x02, 0x02, 0x04, 0x08},
	{0x00, 0x0A, 0x04, 0x1F, 0x04, 0x0A, 0x00},
	{0x00, 0x04, 0x04, 0x1F, 0x04, 0x04, 0x00},
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x04, 0x08},
	{0x00, 0x00, 0x00, 0x1F, 0x00, 0x00, 0x00},
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x04, 0x00},
	{0x01, 0x02, 0x04, 0x08, 0x10, 0x00, 0x00},
	{0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E},
	{0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E},
	{0x0E, 0x11, 0x01, 0x06, 0x08, 0x10, 0x1F},
	{0x1F, 0x01, 0x02, 0x06, 0x01, 0x11, 0x0E},
	{0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02},
	{0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E},
	{0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E},
	{0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	{0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E},
	{0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C},
	{0x00, 0x00, 0x04, 0x00, 0x00, 0x04, 0x00},
	{0x00, 0x00, 0x04, 0x00, 0x00, 0x04, 0x08},
	{0x02, 0x04, 0x08, 0x10, 0x08, 0x04, 0x02},
	{0x00, 0x00, 0x1F, 0x00, 0x1F, 0x00, 0x00},
	{0x08, 0x04, 0x02, 0x01, 0x02, 0x04, 0x08},
	{0x0E, 0x11, 0x01, 0x02, 0x04, 0x00, 0x04},
	{0x0E, 0x11, 0x17, 0x15, 0x17, 0x10, 0x0E},
	{0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	{0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11, 0x1E},
	{0x0E, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0E},
	{0x1C, 0x12, 0x11, 0x11, 0x11, 0x12, 0x1C},
	{0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x1F},
	{0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10},
	{0x0E, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0F},
	{0x11, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	{0x0E, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0E},
	{0x01, 0x01, 0x01, 0x01, 0x11, 0x11, 0x0E},
	{0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	{0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F},
	{0x11, 0x1B, 0x15, 0x15, 0x11, 0x11, 0x11},
	{0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	{0x0E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	{0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10},
	{0x0E, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0D},
	{0x1E, 0x11, 0x11, 0x1E, 0x14, 0x12, 0x11},
	{0x0E, 0x11, 0x10, 0x0E, 0x01, 0x11, 0x0E},
	{0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	{0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	{0x11, 0x11, 0x11, 0x11, 0x11, 0x0A, 0x04},
	{0x11, 0x11, 0x11, 0x15, 0x15, 0x1B, 0x11},
	{0x11, 0x11, 0x0A, 0x04, 0x0A, 0x11, 0x11},
	{0x11, 0x11, 0x0A, 0x04, 0x04, 0x04, 0x04},
	{0x1F, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1F},
	{0x0E, 0x08, 0x08, 0x08, 0x08, 0x08, 0x0E},
	{0x10, 0x08, 0x04, 0x02, 0x01, 0x00, 0x00},
	{0x0E, 0x02, 0x02, 0x02, 0x02, 0x02, 0x0E},
	{0x04, 0x0A, 0x11, 0x00, 0x00, 0x00, 0x00},
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x1F},
	{0x08, 0x04, 0x02, 0x00, 0x00, 0x00, 0x00},
	{0x00, 0x00, 0x0E, 0x01, 0x0F, 0x11, 0x0F},
	{0x10, 0x10, 0x1E, 0x11, 0x11, 0x11, 0x1E},
	{0x00, 0x00, 0x0E, 0x11, 0x10, 0x11, 0x0E},
	{0x01, 0x01, 0x0F, 0x11, 0x11, 0x11, 0x0F},
	{0x00, 0x00, 0x0E, 0x11, 0x1F, 0x10, 0x0E},
	{0x06, 0x08, 0x1C, 0x08, 0x08, 0x08, 0x08},
	{0x00, 0x00, 0x0F, 0x11, 0x0F, 0x01, 0x0E},
	{0x10, 0x10, 0x1E, 0x11, 0x11, 0x11, 0x11},
	{0x04, 0x00, 0x0C, 0x04, 0x04, 0x04, 0x0E},
	{0x02, 0x00, 0x06, 0x02, 0x02, 0x12, 0x0C},
	{0x10, 0x10, 0x12, 0x14, 0x18, 0x14, 0x12},
	{0x0C, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0E},
	{0x00, 0x00, 0x1A, 0x15, 0x15, 0x11, 0x11},
	{0x00, 0x00, 0x1E, 0x11, 0x11, 0x11, 0x11},
	{0x00, 0x00, 0x0E, 0x11, 0x11, 0x11, 0x0E},
	{0x00, 0x00, 0x1E, 0x11, 0x1E, 0x10, 0x10},
	{0x00, 0x00, 0x0F, 0x11, 0x0F, 0x01, 0x01},
	{0x00, 0x00, 0x16, 0x19, 0x10, 0x10, 0x10},
	{0x00, 0x00, 0x0E, 0x10, 0x0E, 0x01, 0x1E},
	{0x08, 0x08, 0x1C, 0x08, 0x08, 0x08, 0x06},
	{0x00, 0x00, 0x11, 0x11, 0x11, 0x11, 0x0F},
	{0x00, 0x00, 0x11, 0x11, 0x11, 0x0A, 0x04},
	{0x00, 0x00, 0x11, 0x11, 0x15, 0x15, 0x0A},
	{0x00, 0x00, 0x11, 0x0A, 0x04, 0x0A, 0x11},
	{0x00, 0x00, 0x11, 0x11, 0x0F, 0x01, 0x0E},
	{0x00, 0x00, 0x1F, 0x02, 0x04, 0x08, 0x1F},
	{0x02, 0x04, 0x04, 0x08, 0x04, 0x04, 0x02},
	{0x04, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	{0x08, 0x04, 0x04, 0x02, 0x04, 0x04, 0x08},
	{0x00, 0x00, 0x08, 0x15, 0x02, 0x00, 0x00},
}
