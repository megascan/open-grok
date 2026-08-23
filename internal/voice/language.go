package voice

import (
	"os"
	"strings"
)

// STTLanguage is one official Grok Speech-to-Text catalog entry.
type STTLanguage struct {
	Code string
	Name string
}

// AutoLanguage is the client-only sentinel. Never send it on the STT wire.
const AutoLanguage = "auto"

// DefaultLanguage is used when the stored value is empty or unknown.
const DefaultLanguage = "en"

// STTLanguages is the public Grok STT catalog, sorted by English name.
var STTLanguages = []STTLanguage{
	{Code: "ar", Name: "Arabic"},
	{Code: "cs", Name: "Czech"},
	{Code: "da", Name: "Danish"},
	{Code: "nl", Name: "Dutch"},
	{Code: "en", Name: "English"},
	{Code: "fil", Name: "Filipino"},
	{Code: "fr", Name: "French"},
	{Code: "de", Name: "German"},
	{Code: "hi", Name: "Hindi"},
	{Code: "id", Name: "Indonesian"},
	{Code: "it", Name: "Italian"},
	{Code: "ja", Name: "Japanese"},
	{Code: "ko", Name: "Korean"},
	{Code: "mk", Name: "Macedonian"},
	{Code: "ms", Name: "Malay"},
	{Code: "fa", Name: "Persian"},
	{Code: "pl", Name: "Polish"},
	{Code: "pt", Name: "Portuguese"},
	{Code: "ro", Name: "Romanian"},
	{Code: "ru", Name: "Russian"},
	{Code: "es", Name: "Spanish"},
	{Code: "sv", Name: "Swedish"},
	{Code: "th", Name: "Thai"},
	{Code: "tr", Name: "Turkish"},
	{Code: "vi", Name: "Vietnamese"},
}

// SettingsLanguageCodes is the settings-modal enum: System (auto) then catalog codes.
func SettingsLanguageCodes() []string {
	out := make([]string, 0, 1+len(STTLanguages))
	out = append(out, AutoLanguage)
	for _, lang := range STTLanguages {
		out = append(out, lang.Code)
	}
	return out
}

// LanguageByCode looks up a catalog entry by exact (case-sensitive) code.
func LanguageByCode(code string) (STTLanguage, bool) {
	for _, lang := range STTLanguages {
		if lang.Code == code {
			return lang, true
		}
	}
	return STTLanguage{}, false
}

// CanonicalizeLanguage maps a user/config string to a catalog code or AutoLanguage.
func CanonicalizeLanguage(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return DefaultLanguage
	}
	if strings.EqualFold(raw, AutoLanguage) {
		return AutoLanguage
	}
	if code := matchSupported(raw); code != "" {
		return code
	}
	primary := primarySubtag(raw)
	if code := matchSupported(primary); code != "" {
		return code
	}
	if aliased := aliasToSupported(primary); aliased != "" {
		return aliased
	}
	return DefaultLanguage
}

// LanguageForAPI is the concrete catalog code to send on the STT wire.
// It never returns "auto".
func LanguageForAPI(stored string) string {
	canonical := CanonicalizeLanguage(stored)
	if canonical == AutoLanguage {
		if sys := systemLanguage(); sys != "" {
			return sys
		}
		return DefaultLanguage
	}
	return canonical
}

func systemLanguage() string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		loc := strings.TrimSpace(os.Getenv(key))
		if loc == "" {
			continue
		}
		if strings.EqualFold(loc, "C") || strings.EqualFold(loc, "POSIX") {
			return ""
		}
		primary := primarySubtag(loc)
		if code := matchSupported(primary); code != "" {
			return code
		}
		if aliased := aliasToSupported(primary); aliased != "" {
			return aliased
		}
		return ""
	}
	return ""
}

func primarySubtag(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, sep := range []byte{'_', '-', '.'} {
		if i := strings.IndexByte(raw, sep); i >= 0 {
			raw = raw[:i]
		}
	}
	return strings.TrimSpace(raw)
}

func matchSupported(raw string) string {
	for _, lang := range STTLanguages {
		if strings.EqualFold(raw, lang.Code) {
			return lang.Code
		}
	}
	return ""
}

func aliasToSupported(primary string) string {
	if strings.EqualFold(primary, "tl") {
		return "fil"
	}
	return ""
}
