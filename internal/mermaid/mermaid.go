// Package mermaid renders Mermaid diagram source to a PNG through an
// injectable layout engine and SVG rasterizer.
//
// There is no Chromium, no mermaid CLI, and no network: the default engine
// lays out a simple flowchart/graph as SVG, and the default rasterizer paints
// that SVG with the Go standard library (image/png). Exotic diagram types
// return an unsupported error so the pager can fall back to a code block.
//
// # Pager glue
//
// Call [RenderToFile] instead of pager.writeMermaidPNG. Unlike writeMermaidPNG,
// the first argument is Mermaid SOURCE (not ASCII-art of the diagram).
//
//	// current (glyph-raster of ASCII art):
//	writeMermaidPNG(art, out, dark, quality)
//	// replacement (real SVG layout + raster):
//	mermaid.RenderToFile(source, out, mermaid.Params{Dark: dark, Quality: quality, TargetWidthPx: widthPx})
//
// Call sites today: internal/pager/mermaid_worker.go
// runMermaidRenderChild (~line 256) and the clickMermaid fallback (~line 346).
package mermaid

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultMaxSourceBytes is the render_checked cap (64 KiB), matching
// xai-grok-mermaid RenderLimits::default.
const DefaultMaxSourceBytes = 64 * 1024

// MAX_OUTPUT_MEGAPIXELS is the raster area ceiling (32 MP), matching
// xai-grok-mermaid::MAX_OUTPUT_MEGAPIXELS.
const MAX_OUTPUT_MEGAPIXELS = 32.0

// Surface colors matching xai-grok-mermaid LIGHT_SURFACE / DARK_SURFACE.
const (
	LightSurfaceHex = "#FAFAFA"
	DarkSurfaceHex  = "#18181B"
)

// Params controls a single diagram render.
//
// TargetWidthPx is the primary size driver (already HiDPI-oversampled by the
// caller when set). Quality=="open" is the OS-viewer path: when TargetWidthPx
// is 0 it selects a larger default width, matching pager quality=="open".
type Params struct {
	Dark          bool
	Quality       string
	TargetWidthPx int
	MaxHeightPx   int // 0 = default (4096, or 8192 when Quality=="open")
}

// Limits are applied by [RenderChecked] before the engine so untrusted source
// cannot exhaust memory via an oversized payload.
type Limits struct {
	// MaxSourceBytes is the maximum accepted source length in bytes.
	// Larger input is rejected with KindUnsupported without invoking the engine.
	// Zero means [DefaultMaxSourceBytes].
	MaxSourceBytes int
}

// DefaultLimits returns the 64 KiB source cap used by [RenderToFile].
func DefaultLimits() Limits {
	return Limits{MaxSourceBytes: DefaultMaxSourceBytes}
}

// Diagram is a rendered PNG plus the exact raster dimensions.
type Diagram struct {
	PNG      []byte
	WidthPx  int
	HeightPx int
}

// Kind classifies a render failure. Every kind maps to the same user-facing
// outcome (fall back to the source code block); they differ for observability.
type Kind int

const (
	KindParse Kind = iota
	KindLayout
	KindRasterize
	KindUnsupported
	KindPanic
)

// Error is a mermaid render failure.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string {
	if e == nil {
		return "mermaid error"
	}
	switch e.Kind {
	case KindParse:
		return "mermaid parse error: " + e.Msg
	case KindLayout:
		return "mermaid layout error: " + e.Msg
	case KindRasterize:
		return "mermaid rasterize error: " + e.Msg
	case KindUnsupported:
		return "mermaid render unsupported: " + e.Msg
	case KindPanic:
		return "mermaid engine panicked: " + e.Msg
	default:
		return "mermaid error: " + e.Msg
	}
}

// IsUnsupported reports whether err is an unsupported-diagram / resource-limit error.
func IsUnsupported(err error) bool {
	var e *Error
	if err == nil {
		return false
	}
	if ee, ok := err.(*Error); ok {
		e = ee
	} else {
		return false
	}
	return e.Kind == KindUnsupported
}

func (p Params) withDefaults() Params {
	if p.TargetWidthPx <= 0 {
		if p.Quality == "open" {
			p.TargetWidthPx = 2560
		} else {
			p.TargetWidthPx = 1024
		}
	}
	if p.MaxHeightPx <= 0 {
		if p.Quality == "open" {
			p.MaxHeightPx = 8192
		} else {
			p.MaxHeightPx = 4096
		}
	}
	return p
}

func (l Limits) withDefaults() Limits {
	if l.MaxSourceBytes <= 0 {
		l.MaxSourceBytes = DefaultMaxSourceBytes
	}
	return l
}

// RenderChecked renders source with eng, enforcing limits before the engine
// runs and converting an engine panic into KindPanic.
//
// Source larger than Limits.MaxSourceBytes is rejected with KindUnsupported
// without invoking the engine.
func RenderChecked(eng Engine, source string, params Params, limits Limits) (diag Diagram, err error) {
	limits = limits.withDefaults()
	if len(source) > limits.MaxSourceBytes {
		return Diagram{}, &Error{
			Kind: KindUnsupported,
			Msg:  fmt.Sprintf("source is %d bytes, over the %d-byte limit", len(source), limits.MaxSourceBytes),
		}
	}
	if eng == nil {
		return Diagram{}, &Error{Kind: KindUnsupported, Msg: "nil engine"}
	}
	defer func() {
		if rec := recover(); rec != nil {
			diag = Diagram{}
			err = &Error{Kind: KindPanic, Msg: fmt.Sprint(rec)}
		}
	}()
	return eng.Render(source, params)
}

// RenderToFile is the pager entry point: mermaid SOURCE in, PNG file out.
//
// It uses the default flowchart engine and stdlib SVG rasterizer, enforces
// the 64 KiB source cap, and writes out with mode 0600 (matching
// writeMermaidPNG). Parent directories are created at 0700 when missing.
func RenderToFile(source, out string, params Params) error {
	d, err := RenderChecked(NewFlowchartEngine(nil), source, params, DefaultLimits())
	if err != nil {
		return err
	}
	if dir := filepath.Dir(out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(d.PNG)
	return err
}
