package worker

import (
	"testing"

	"quicksend/internal/campaign"
)

func TestBuildMessageFormats(t *testing.T) {
	text := buildMessage(&campaign.Campaign{Subject: "s", Body: "plain", BodyFormat: campaign.FormatText}, "me@gmail.com", "to@example.com")
	if text.HTML != "" || text.Text != "plain" {
		t.Errorf("text campaign: html=%q text=%q", text.HTML, text.Text)
	}

	html := buildMessage(&campaign.Campaign{
		Subject: "s", Body: `<img src="cid:logo@x">`, BodyFormat: campaign.FormatHTML,
		Attachments: []campaign.Attachment{
			{Filename: "logo.png", MimeType: "image/png", Content: []byte("p"), ContentID: "logo@x"},
			{Filename: "a.pdf", MimeType: "application/pdf", Content: []byte("d")},
		},
	}, "me@gmail.com", "to@example.com")
	if len(html.Inline) != 1 || html.Inline[0].ContentID != "logo@x" || len(html.Attachments) != 1 || html.Attachments[0].Filename != "a.pdf" {
		t.Errorf("html campaign: inline=%+v attachments=%+v", html.Inline, html.Attachments)
	}
}
