package pager

import (
	"fmt"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Grok Night / Grok Day token colors from
// crates/codegen/xai-grok-pager-render/assets/grok-*.tmTheme.
var (
	grokNightHL = chroma.MustNewStyle("grok-night", chroma.StyleEntries{
		chroma.Background:            "#0e0e0e",
		chroma.Text:                  "#b2b2b2",
		chroma.Error:                 "#ff5370",
		chroma.Comment:               "italic #51597d",
		chroma.CommentSpecial:        "italic #5a638c",
		chroma.Keyword:               "#bb9af7",
		chroma.KeywordConstant:       "#ff9e64",
		chroma.KeywordDeclaration:    "#bb9af7",
		chroma.KeywordNamespace:      "#bb9af7",
		chroma.KeywordReserved:       "#bb9af7",
		chroma.KeywordType:           "#7dcfff",
		chroma.Name:                  "#c8c8c8",
		chroma.NameAttribute:         "#bb9af7",
		chroma.NameBuiltin:           "#7dcfff",
		chroma.NameBuiltinPseudo:     "#f7768e",
		chroma.NameClass:             "#7aa2f7",
		chroma.NameConstant:          "#ff9e64",
		chroma.NameDecorator:         "#7dcfff",
		chroma.NameEntity:            "#bb9af7",
		chroma.NameException:         "#f7768e",
		chroma.NameFunction:          "#7aa2f7",
		chroma.NameLabel:             "#7aa2f7",
		chroma.NameNamespace:         "#0db9d7",
		chroma.NameTag:               "#f7768e",
		chroma.NameVariable:          "#c8c8c8",
		chroma.NameVariableClass:     "#c8c8c8",
		chroma.NameVariableGlobal:    "#c8c8c8",
		chroma.NameVariableInstance:  "#c8c8c8",
		chroma.LiteralString:         "#9ece6a",
		chroma.LiteralStringAffix:    "#9ece6a",
		chroma.LiteralStringChar:     "#9ece6a",
		chroma.LiteralStringDoc:      "italic #51597d",
		chroma.LiteralStringEscape:   "#89ddff",
		chroma.LiteralStringInterpol: "#9ece6a",
		chroma.LiteralStringRegex:    "#b4f9f8",
		chroma.LiteralNumber:         "#ff9e64",
		chroma.Operator:              "#89ddff",
		chroma.OperatorWord:          "#bb9af7",
		chroma.Punctuation:           "#c8c8c8",
		chroma.GenericDeleted:        "#f7768e",
		chroma.GenericInserted:       "#9ece6a",
		chroma.GenericEmph:           "italic #c8c8c8",
		chroma.GenericStrong:         "bold #c8c8c8",
		chroma.GenericHeading:        "bold #7aa2f7",
		chroma.GenericSubheading:     "#7aa2f7",
	})
	grokDayHL = chroma.MustNewStyle("grok-day", chroma.StyleEntries{
		chroma.Background:          "#f7f7f7",
		chroma.Text:                "#2a2a2a",
		chroma.Error:               "#CD3048",
		chroma.Comment:             "italic #909090",
		chroma.CommentSpecial:      "italic #808080",
		chroma.Keyword:             "#7D4BC6",
		chroma.KeywordConstant:     "#C45A12",
		chroma.KeywordDeclaration:  "#7D4BC6",
		chroma.KeywordNamespace:    "#7D4BC6",
		chroma.KeywordReserved:     "#7D4BC6",
		chroma.KeywordType:         "#0082AA",
		chroma.Name:                "#2a2a2a",
		chroma.NameAttribute:       "#7D4BC6",
		chroma.NameBuiltin:         "#0082AA",
		chroma.NameBuiltinPseudo:   "#CD3048",
		chroma.NameClass:           "#1A5FBF",
		chroma.NameConstant:        "#C45A12",
		chroma.NameDecorator:       "#0082AA",
		chroma.NameFunction:        "#1A5FBF",
		chroma.NameLabel:           "#1A5FBF",
		chroma.NameNamespace:       "#0082AA",
		chroma.NameTag:             "#CD3048",
		chroma.NameVariable:        "#2a2a2a",
		chroma.LiteralString:       "#378E23",
		chroma.LiteralStringDoc:    "italic #909090",
		chroma.LiteralStringEscape: "#0082AA",
		chroma.LiteralStringRegex:  "#0082AA",
		chroma.LiteralNumber:       "#C45A12",
		chroma.Operator:            "#0082AA",
		chroma.OperatorWord:        "#7D4BC6",
		chroma.Punctuation:         "#2a2a2a",
		chroma.GenericDeleted:      "#CD3048",
		chroma.GenericInserted:     "#378E23",
	})
)

var (
	hlThemeMu   sync.RWMutex
	hlThemeName = "groknight"
)

func setHighlightTheme(name string) {
	hlThemeMu.Lock()
	hlThemeName = strings.ToLower(name)
	hlThemeMu.Unlock()
}

func highlightStyle() *chroma.Style {
	hlThemeMu.RLock()
	name := hlThemeName
	hlThemeMu.RUnlock()
	if strings.Contains(name, "day") || strings.Contains(name, "light") {
		return grokDayHL
	}
	return grokNightHL
}

func lexerFor(path, lang string) chroma.Lexer {
	if path != "" {
		if l := lexers.Match(path); l != nil {
			return chroma.Coalesce(l)
		}
	}
	if lang != "" {
		if l := lexers.Get(lang); l != nil {
			return chroma.Coalesce(l)
		}
	}
	return nil
}

// highlightSourceLines paints each source line with chroma using the Grok
// Night/Day token map, matching the Rust pager's syntect path.
func highlightSourceLines(path string, lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	return highlightSource(path, "", strings.Join(lines, "\n"))
}

func highlightByLang(lang string, lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	return highlightSource("", lang, strings.Join(lines, "\n"))
}

func highlightLine(path, line string) string {
	out := highlightSource(path, "", line)
	if len(out) == 0 {
		return line
	}
	return out[0]
}

func highlightSource(path, lang, src string) []string {
	plain := strings.Split(src, "\n")
	lex := lexerFor(path, lang)
	if lex == nil {
		return plain
	}
	it, err := lex.Tokenise(nil, src)
	if err != nil || it == nil {
		return plain
	}
	style := highlightStyle()
	var lines []string
	var cur strings.Builder
	for _, tok := range it.Tokens() {
		if tok.Value == "" {
			continue
		}
		rawParts := strings.Split(tok.Value, "\n")
		entry := style.Get(tok.Type)
		for i, part := range rawParts {
			if i > 0 {
				lines = append(lines, cur.String())
				cur.Reset()
			}
			if part != "" {
				cur.WriteString(paintHLToken(part, entry))
			}
		}
	}
	lines = append(lines, cur.String())
	if len(lines) < len(plain) {
		lines = append(lines, plain[len(lines):]...)
	}
	return lines
}

func paintHLToken(s string, e chroma.StyleEntry) string {
	if s == "" {
		return s
	}
	var codes []string
	if e.Colour.IsSet() {
		codes = append(codes, fmt.Sprintf("38;2;%d;%d;%d", e.Colour.Red(), e.Colour.Green(), e.Colour.Blue()))
	}
	if e.Bold == chroma.Yes {
		codes = append(codes, "1")
	}
	if e.Italic == chroma.Yes {
		codes = append(codes, "3")
	}
	if e.Underline == chroma.Yes {
		codes = append(codes, "4")
	}
	if len(codes) == 0 {
		return s
	}
	return "\x1b[" + strings.Join(codes, ";") + "m" + s + "\x1b[0m"
}
