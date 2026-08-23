package mermaid

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type stubRaster struct {
	svgs []string
}

func (s *stubRaster) Rasterize(svg string, _ Params) (Diagram, error) {
	s.svgs = append(s.svgs, svg)
	return encodeTinyPNG(8, 8)
}

func encodeTinyPNG(w, h int) (Diagram, error) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{0x20, 0x80, 0xC0, 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return Diagram{}, err
	}
	return Diagram{PNG: buf.Bytes(), WidthPx: w, HeightPx: h}, nil
}

type spyEngine struct {
	called atomic.Bool
}

func (s *spyEngine) Render(string, Params) (Diagram, error) {
	s.called.Store(true)
	panic("engine must not be called for oversized source")
}

type okEngine struct {
	called atomic.Bool
}

func (s *okEngine) Render(string, Params) (Diagram, error) {
	s.called.Store(true)
	return Diagram{PNG: []byte{1, 2, 3}, WidthPx: 10, HeightPx: 20}, nil
}

func TestRenderCheckedOversizedDoesNotCallEngine(t *testing.T) {
	eng := &spyEngine{}
	_, err := RenderChecked(eng, "this source is definitely longer than eight bytes", Params{}, Limits{MaxSourceBytes: 8})
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsUnsupported(err) {
		t.Fatalf("want KindUnsupported, got %v", err)
	}
	if eng.called.Load() {
		t.Fatal("engine must not run when the source is over the limit")
	}
}

func TestRenderCheckedSourceAtLimitIsAccepted(t *testing.T) {
	eng := &okEngine{}
	src := "12345678"
	d, err := RenderChecked(eng, src, Params{}, Limits{MaxSourceBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if d.WidthPx != 10 || !eng.called.Load() {
		t.Fatalf("diagram=%+v called=%v", d, eng.called.Load())
	}
}

func TestRenderCheckedPassesThroughEngineSuccess(t *testing.T) {
	eng := &okEngine{}
	d, err := RenderChecked(eng, "flowchart LR\nA-->B", Params{}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if d.WidthPx != 10 || d.HeightPx != 20 {
		t.Fatalf("%+v", d)
	}
	if !eng.called.Load() {
		t.Fatal("engine not called")
	}
}

func TestRenderCheckedPanicBecomesPanicError(t *testing.T) {
	eng := &spyEngine{} // panics on Render
	_, err := RenderChecked(eng, "flowchart LR\nA-->B", Params{}, DefaultLimits())
	if err == nil {
		t.Fatal("expected panic error")
	}
	e, ok := err.(*Error)
	if !ok || e.Kind != KindPanic {
		t.Fatalf("want KindPanic, got %v", err)
	}
	if !strings.Contains(e.Msg, "engine must not be called") {
		t.Fatalf("payload: %v", err)
	}
}

func TestStubRasterizerRecordsSVGNotASCIIArt(t *testing.T) {
	src, err := os.ReadFile("testdata/flowchart_lr.mmd")
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubRaster{}
	eng := NewFlowchartEngine(stub)
	d, err := RenderChecked(eng, string(src), Params{TargetWidthPx: 200}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if d.WidthPx != 8 || len(d.PNG) < 8 || !bytes.Equal(d.PNG[:8], pngMagic) {
		t.Fatalf("stub png: w=%d n=%d", d.WidthPx, len(d.PNG))
	}
	if len(stub.svgs) != 1 {
		t.Fatalf("recorded %d svgs", len(stub.svgs))
	}
	svg := stub.svgs[0]
	for _, need := range []string{"<svg", "</svg>", "Start", "Finish", "<rect", "<line"} {
		if !strings.Contains(svg, need) {
			t.Fatalf("svg missing %q:\n%s", need, svg)
		}
	}
	if strings.Contains(svg, "┌") || strings.Contains(svg, "─") {
		t.Fatal("layout must be SVG shapes, not unicode box-drawing art")
	}
}

func TestExoticDiagramsUnsupported(t *testing.T) {
	stub := &stubRaster{}
	eng := NewFlowchartEngine(stub)
	for _, src := range []string{
		"sequenceDiagram\n  Alice->>Bob: hi",
		"pie title Pets\n  \"Dogs\" : 386",
		"classDiagram\n  class Animal",
		"gantt\n  title A",
		"erDiagram\n  A ||--o{ B : has",
	} {
		_, err := RenderChecked(eng, src, Params{}, DefaultLimits())
		if !IsUnsupported(err) {
			t.Fatalf("%q: want unsupported, got %v", src, err)
		}
	}
	if len(stub.svgs) != 0 {
		t.Fatal("rasterizer must not run for unsupported diagrams")
	}
}

func TestFlowchartAndGraphSupported(t *testing.T) {
	stub := &stubRaster{}
	eng := NewFlowchartEngine(stub)
	for _, src := range []string{
		"flowchart LR\nA-->B",
		"graph TD\nA-->B",
		"flowchart LR\nA[Hello] --> B[World]",
		"flowchart LR\nA-->B-->C",
	} {
		if _, err := RenderChecked(eng, src, Params{}, DefaultLimits()); err != nil {
			t.Fatalf("%q: %v", src, err)
		}
	}
}

func TestLightAndDarkSVGSurfacesDiffer(t *testing.T) {
	stub := &stubRaster{}
	eng := NewFlowchartEngine(stub)
	src := "flowchart LR\nA-->B"
	if _, err := eng.Layout(src, Params{Dark: false}); err != nil {
		t.Fatal(err)
	}
	light, err := eng.Layout(src, Params{Dark: false})
	if err != nil {
		t.Fatal(err)
	}
	dark, err := eng.Layout(src, Params{Dark: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(light, LightSurfaceHex) {
		t.Fatalf("light surface missing: %s", light)
	}
	if !strings.Contains(dark, DarkSurfaceHex) {
		t.Fatalf("dark surface missing: %s", dark)
	}
	if light == dark {
		t.Fatal("themes must change the SVG")
	}
}

func TestDefaultLimitsIs64KiB(t *testing.T) {
	if DefaultLimits().MaxSourceBytes != 64*1024 || DefaultMaxSourceBytes != 64*1024 {
		t.Fatalf("cap %d", DefaultLimits().MaxSourceBytes)
	}
}

func TestRenderToFileWritesPNG(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "nested", "d.png")
	src, err := os.ReadFile("testdata/flowchart_lr.mmd")
	if err != nil {
		t.Fatal(err)
	}
	if err := RenderToFile(string(src), out, Params{Dark: true, Quality: "open", TargetWidthPx: 200}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 8 || !bytes.Equal(b[:8], pngMagic) {
		t.Fatal("not a png")
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 200 {
		t.Fatalf("width %d", img.Bounds().Dx())
	}
	if img.Bounds().Dy() < 1 {
		t.Fatal("empty height")
	}
}

func TestStdlibRasterizerBlueRect(t *testing.T) {
	svg, err := os.ReadFile("testdata/blue_rect.svg")
	if err != nil {
		t.Fatal(err)
	}
	d, err := StdlibRasterizer{}.Rasterize(string(svg), Params{TargetWidthPx: 200, MaxHeightPx: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	if d.WidthPx != 200 || d.HeightPx != 100 {
		t.Fatalf("dims %dx%d", d.WidthPx, d.HeightPx)
	}
	img, err := png.Decode(bytes.NewReader(d.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 200 || img.Bounds().Dy() != 100 {
		t.Fatalf("decoded %v", img.Bounds())
	}
	// 10x10 blue square in a 100x50 SVG scaled 2x => 20x20 blue at top-left.
	c := img.At(4, 4).(color.RGBA)
	if int(c.B) < 200 || int(c.R) > 40 {
		t.Fatalf("expected blue pixel, got %+v", c)
	}
}

func TestStdlibRasterizerIgnoresRemoteImage(t *testing.T) {
	svg, err := os.ReadFile("testdata/remote_image.svg")
	if err != nil {
		t.Fatal(err)
	}
	d, err := StdlibRasterizer{}.Rasterize(string(svg), Params{TargetWidthPx: 20, MaxHeightPx: 20})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(d.PNG))
	if err != nil {
		t.Fatal(err)
	}
	c := img.At(10, 10).(color.RGBA)
	if int(c.G) < 200 || int(c.R) > 40 {
		t.Fatalf("expected green fill after skipping remote image, got %+v", c)
	}
}

func TestGarbageInputNeverPanics(t *testing.T) {
	eng := NewFlowchartEngine(&stubRaster{})
	for _, garbage := range []string{
		"",
		"@@@@",
		"%% only a comment",
		"flowchart\n\n\n",
		"????????",
		"\u0000\u0001\u0002\u0003",
		"flowchart LR\n  A[unterminated --> ",
		"pie\n  : :",
		"erDiagram\n  A ||",
		"sequenceDiagram\n  A->>",
	} {
		_, err := RenderChecked(eng, garbage, Params{}, DefaultLimits())
		if e, ok := err.(*Error); ok && e.Kind == KindPanic {
			t.Fatalf("panicked on %q: %v", garbage, err)
		}
	}
}

func TestPackageHasNoNetworkOrChromium(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{
		`"net"`, `"net/http"`, `"net/url"`, `"os/exec"`,
		`"github.com/chromedp/chromedp"`, `"github.com/go-rod/rod"`,
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		imps := importBlock(string(b))
		for _, w := range banned {
			if strings.Contains(imps, w) {
				t.Fatalf("%s imports banned %s", e.Name(), w)
			}
		}
	}
}

func importBlock(src string) string {
	start := strings.Index(src, "\nimport ")
	if start < 0 {
		start = strings.Index(src, "\nimport(")
	}
	if start < 0 {
		return ""
	}
	rest := src[start:]
	if i := strings.Index(rest, ")"); i >= 0 {
		return rest[:i+1]
	}
	nl := strings.Index(rest[1:], "\n")
	if nl < 0 {
		return rest
	}
	return rest[:nl+1]
}

var pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
