package pager

import (
	_ "embed"
	"strings"

	"github.com/megascan/open-grok/internal/config"
)

//go:embed logo05.txt
var logoSmall string

//go:embed logo07.txt
var logoFull string

func (m *model) welcomeLogo() string {
	src := ""
	switch {
	case m.height >= 26:
		src = strings.TrimRight(logoFull, "\n")
	case m.height >= 22:
		src = strings.TrimRight(logoSmall, "\n")
	default:
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString("  " + titleStyle.Render(line) + "\n")
	}
	b.WriteByte('\n')
	return b.String()
}

func privacyBanner() string {
	var b strings.Builder
	b.WriteString(claimStyle.Render("  Help improve Entropy") + "\n")
	b.WriteString(dimStyle.Render("  Off by default. Opt-in to allow coding data (prompts, traces, metrics)") + "\n")
	b.WriteString(dimStyle.Render("  for training and debugging. Change anytime via /privacy.") + "\n")
	b.WriteString(dimStyle.Render("  [Opt in]  /privacy    [Opt out]  dismiss this banner") + "\n")
	return b.String()
}

func (m *model) showPrivacyBanner() bool {
	if m.unsignedIn() || m.trustPending {
		return false
	}
	if m.screenMd == modeMinimal {
		return false
	}
	if m.cfg.Prefs != nil && m.cfg.Prefs["privacy_banner_acked"] != "" {
		return false
	}
	return m.cfg.UI.PrivacyOptOut || m.cfg.Prefs == nil || m.cfg.Prefs["privacy_opt_in"] != "true"
}

func (m *model) ackPrivacyBanner(optIn bool) {
	if m.cfg.Prefs == nil {
		m.cfg.Prefs = config.Prefs{}
	}
	m.cfg.Prefs["privacy_banner_acked"] = "true"
	if optIn {
		m.cfg.UI.PrivacyOptOut = false
		m.cfg.Prefs["privacy_opt_in"] = "true"
	} else {
		m.cfg.UI.PrivacyOptOut = true
	}
	_ = config.Save(m.cfg)
	m.layoutChat()
}

func announcementBanner() string {
	return claimStyle.Render("  [Entropy]") + dimStyle.Render("        hide: /announcements hide  [hide]") + "\n"
}

func (m *model) showAnnouncementBanner() bool {
	if m.sess == nil || m.screenMd == modeMinimal {
		return false
	}
	if m.cfg.Prefs != nil && m.cfg.Prefs["announcements_hidden"] == "true" {
		return false
	}
	return true
}

func (m *model) setAnnouncementsHidden(hidden bool) {
	if m.cfg.Prefs == nil {
		m.cfg.Prefs = config.Prefs{}
	}
	if hidden {
		m.cfg.Prefs["announcements_hidden"] = "true"
	} else {
		delete(m.cfg.Prefs, "announcements_hidden")
	}
	_ = config.Save(m.cfg)
	m.layoutChat()
}
