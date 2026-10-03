package worker

import (
	"strings"
	"testing"

	"quicksend/internal/campaign"
)

func TestBuildMessageFormats(t *testing.T) {
	text := buildMessage(&campaign.Campaign{Subject: "s", Body: "plain", BodyFormat: campaign.FormatText}, "me@gmail.com", "to@example.com", "https://airletter.test")
	if text.HTML != "" || text.Text != "plain" {
		t.Errorf("text campaign: html=%q text=%q", text.HTML, text.Text)
	}

	html := buildMessage(&campaign.Campaign{
		Subject: "s", Body: `<img src="cid:logo@x">`, BodyFormat: campaign.FormatHTML,
		Attachments: []campaign.Attachment{
			{Filename: "logo.png", MimeType: "image/png", Content: []byte("p"), ContentID: "logo@x"},
			{Filename: "a.pdf", MimeType: "application/pdf", Content: []byte("d")},
		},
	}, "me@gmail.com", "to@example.com", "https://airletter.test")
	if len(html.Inline) != 1 || html.Inline[0].ContentID != "logo@x" || len(html.Attachments) != 1 || html.Attachments[0].Filename != "a.pdf" {
		t.Errorf("html campaign: inline=%+v attachments=%+v", html.Inline, html.Attachments)
	}
}

func TestTrialFooter(t *testing.T) {
	const site = "https://airletter.test"
	paid := buildMessage(&campaign.Campaign{Body: "<p>hi</p>"}, "me@gmail.com", "to@example.com", site)
	if strings.Contains(paid.HTML, "Airletter") {
		t.Errorf("paid campaign got the footer: %q", paid.HTML)
	}

	frag := buildMessage(&campaign.Campaign{Body: "<p>hi</p>", Branded: true, Locale: "ru"}, "me@gmail.com", "to@example.com", site)
	if !strings.HasPrefix(frag.HTML, "<p>hi</p><table") || !strings.Contains(frag.HTML, "Разослано с помощью") || !strings.Contains(frag.HTML, site+"/ru?") {
		t.Errorf("fragment footer: %q", frag.HTML)
	}

	full := buildMessage(&campaign.Campaign{Body: "<html><BODY><p>hi</p></BODY></html>", Branded: true, Locale: "en"}, "me@gmail.com", "to@example.com", site)
	if !strings.HasSuffix(full.HTML, "</td></tr></table></BODY></html>") || !strings.Contains(full.HTML, "Sent with") {
		t.Errorf("template footer: %q", full.HTML)
	}

	text := buildMessage(&campaign.Campaign{Body: "hi", BodyFormat: campaign.FormatText, Branded: true, Locale: "xx"}, "me@gmail.com", "to@example.com", site)
	if text.Text != "hi\n\n--\nSent with Airletter: "+site+"/en?utm_source=email&utm_medium=footer&utm_campaign=trial\n" {
		t.Errorf("text footer: %q", text.Text)
	}
}
