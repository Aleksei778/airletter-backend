package worker

import (
	"fmt"
	"html"
	"regexp"

	"airletter/internal/mailer"
)

type footerLabels struct {
	unsubscribe string
	sentWith    string
}

var footerText = map[string]footerLabels{
	"ru": {unsubscribe: "Отписаться от этих писем", sentWith: "Разослано с помощью"},
	"en": {unsubscribe: "Unsubscribe from these emails", sentWith: "Sent with"},
}

var closingBody = regexp.MustCompile(`(?i)</body\s*>`)

// addFooter appends the unsubscribe link that every email carries and, for
// trial campaigns (branded), the "Sent with Airletter" line.
// In HTML it goes before </body> of a full template, or at the end otherwise.
func addFooter(m *mailer.Message, locale, siteURL, unsubToken string, branded bool) {
	label, ok := footerText[locale]
	if !ok {
		locale, label = "en", footerText["en"]
	}
	unsubLink := fmt.Sprintf("%s/%s/unsubscribe/%s", siteURL, locale, unsubToken)
	siteLink := fmt.Sprintf("%s/%s?utm_source=email&utm_medium=footer&utm_campaign=trial", siteURL, locale)

	if m.HTML == "" {
		m.Text += fmt.Sprintf("\n\n--\n%s: %s\n", label.unsubscribe, unsubLink)
		if branded {
			m.Text += fmt.Sprintf("%s Airletter: %s\n", label.sentWith, siteLink)
		}
		return
	}

	content := fmt.Sprintf(`<a href="%s" style="color:#8a8a8a;text-decoration:underline">%s</a>`,
		html.EscapeString(unsubLink), html.EscapeString(label.unsubscribe))
	if branded {
		content += fmt.Sprintf(`<br>%s <a href="%s" style="color:#000000;font-weight:bold;text-decoration:none">Airletter</a>`,
			html.EscapeString(label.sentWith), html.EscapeString(siteLink))
	}
	footer := `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin-top:32px">` +
		`<tr><td align="center" style="padding:16px 0;border-top:1px solid #e6e6e6;font-family:Arial,Helvetica,sans-serif;font-size:12px;line-height:18px;color:#8a8a8a">` +
		content + `</td></tr></table>`

	if loc := closingBody.FindAllStringIndex(m.HTML, -1); len(loc) > 0 {
		at := loc[len(loc)-1][0]
		m.HTML = m.HTML[:at] + footer + m.HTML[at:]
		return
	}
	m.HTML += footer
}
