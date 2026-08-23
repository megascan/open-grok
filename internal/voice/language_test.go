package voice

import (
	"strings"
	"testing"
)

func TestCanonicalizeLanguage(t *testing.T) {
	cases := map[string]string{
		"":            "en",
		"  ":          "en",
		"en":          "en",
		"EN":          "en",
		"  fr ":       "fr",
		"auto":        "auto",
		"AUTO":        "auto",
		"en-US":       "en",
		"pt_BR.UTF-8": "pt",
		"fil":         "fil",
		"tl":          "fil",
		"tl-PH":       "fil",
		"zh":          "en",
		"zh-Hans":     "en",
		"nope":        "en",
	}
	for in, want := range cases {
		if got := CanonicalizeLanguage(in); got != want {
			t.Errorf("CanonicalizeLanguage(%q)=%q want %q", in, got, want)
		}
	}
}

func TestLanguageForAPINeverAuto(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "C")
	if got := LanguageForAPI("auto"); got == "auto" {
		t.Fatal("LanguageForAPI(auto) must not return auto")
	}
	if got := LanguageForAPI("ja"); got != "ja" {
		t.Fatalf("ja: %q", got)
	}
	if got := LanguageForAPI("EN"); got != "en" {
		t.Fatalf("EN: %q", got)
	}
	if got := LanguageForAPI(""); got != "en" {
		t.Fatalf("empty: %q", got)
	}
	if got := LanguageForAPI("xx"); got != "en" {
		t.Fatalf("unknown: %q", got)
	}
}

func TestLanguageForAPIUsesLocale(t *testing.T) {
	t.Setenv("LC_ALL", "ja_JP.UTF-8")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "")
	if got := LanguageForAPI("auto"); got != "ja" {
		t.Fatalf("got %q", got)
	}
}

func TestLanguageByCodeExact(t *testing.T) {
	if _, ok := LanguageByCode("en"); !ok {
		t.Fatal("en")
	}
	if _, ok := LanguageByCode("EN"); ok {
		t.Fatal("EN must not match")
	}
	if _, ok := LanguageByCode("auto"); ok {
		t.Fatal("auto is not a catalog code")
	}
}

func TestSettingsLanguageCodes(t *testing.T) {
	codes := SettingsLanguageCodes()
	if codes[0] != AutoLanguage {
		t.Fatalf("first %q", codes[0])
	}
	if !strings.Contains(strings.Join(codes, ","), ",fil,") && !contains(codes, "fil") {
		t.Fatalf("missing fil: %v", codes)
	}
	if contains(codes, "zh") {
		t.Fatal("zh is not in the STT catalog")
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
