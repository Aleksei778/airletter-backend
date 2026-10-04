package worker

import (
	"fmt"
	"html"
	"regexp"

	"airletter/internal/mailer"
)

var footerText = map[string]string{
	"ru": "Разослано с помощью",
	"en": "Sent with",
}

var closingBody = regexp.MustCompile(`(?i)</body\s*>`)

// addFooter appends the "Sent with Airletter" line that trial campaigns carry.
// In HTML it goes before </body> of a full template, or at the end otherwise.
func addFooter(m *mailer.Message, locale, siteURL string) {
	label, ok := footerText[locale]
	if !ok {
		locale, label = "en", footerText["en"]
	}
	link := fmt.Sprintf("%s/%s?utm_source=email&utm_medium=footer&utm_campaign=trial", siteURL, locale)

	if m.HTML == "" {
		m.Text += fmt.Sprintf("\n\n--\n%s Airletter: %s\n", label, link)
		return
	}

	footer := fmt.Sprintf(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="margin-top:32px">`+
		`<tr><td align="center" style="padding:16px 0;border-top:1px solid #e6e6e6;font-family:Arial,Helvetica,sans-serif;font-size:12px;line-height:18px;color:#8a8a8a">`+
		`%s <a href="%s" style="color:#000000;font-weight:bold;text-decoration:none">Airletter</a>`+
		`</td></tr></table>`, html.EscapeString(label), html.EscapeString(link))

	if loc := closingBody.FindAllStringIndex(m.HTML, -1); len(loc) > 0 {
		at := loc[len(loc)-1][0]
		m.HTML = m.HTML[:at] + footer + m.HTML[at:]
		return
	}
	m.HTML += footer
}
