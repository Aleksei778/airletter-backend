package mailer

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestBuildWithAttachment(t *testing.T) {
	raw, err := Build(Message{
		From:    mail.Address{Name: "Иван Петров", Address: "ivan@example.com"},
		To:      "bob@example.com",
		Subject: "Привет, мир",
		HTML:    "<p>Hello <b>Bob</b></p><p>Line&nbsp;2</p>",
		Attachments: []Attachment{
			{Filename: "отчёт.pdf", MimeType: "application/pdf", Content: []byte(strings.Repeat("x", 200))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	dec := new(mime.WordDecoder)
	subj, _ := dec.DecodeHeader(msg.Header.Get("Subject"))
	if subj != "Привет, мир" {
		t.Errorf("subject = %q", subj)
	}
	from, err := msg.Header.AddressList("From")
	if err != nil || from[0].Name != "Иван Петров" {
		t.Errorf("from = %v, %v", from, err)
	}

	mt, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if mt != "multipart/mixed" {
		t.Fatalf("content-type = %s", mt)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])

	body, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body.Header.Get("Content-Type"), "multipart/alternative") {
		t.Errorf("body part = %s", body.Header.Get("Content-Type"))
	}

	att, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if att.FileName() != "отчёт.pdf" {
		t.Errorf("filename = %q", att.FileName())
	}
	if _, err := io.ReadAll(att); err != nil {
		t.Fatal(err)
	}
}

func TestBuildWithoutAttachments(t *testing.T) {
	raw, err := Build(Message{
		From: mail.Address{Address: "a@example.com"}, To: "b@example.com",
		Subject: "Hi", HTML: "<p>Hi</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg.Header.Get("Content-Type"), "multipart/alternative") {
		t.Errorf("content-type = %s", msg.Header.Get("Content-Type"))
	}
}

func TestHeaderInjection(t *testing.T) {
	raw, _ := Build(Message{
		From: mail.Address{Address: "a@example.com"}, To: "b@example.com\r\nBcc: evil@example.com",
		Subject: "x", HTML: "x",
	})
	if strings.Contains(string(raw), "\r\nBcc:") {
		t.Error("header injection not prevented")
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText("<p>Hello <b>Bob</b></p><p>A &amp; B</p>")
	if got != "Hello Bob\nA & B" {
		t.Errorf("got %q", got)
	}
}

func TestBuildPlainText(t *testing.T) {
	raw, err := Build(Message{From: mail.Address{Address: "a@example.com"}, To: "b@example.com", Subject: "x", Text: "Привет\nмир"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("content-type = %s", msg.Header.Get("Content-Type"))
	}
	if strings.Contains(string(raw), "text/html") {
		t.Error("plain-text message must not have an HTML part")
	}
}

func TestBuildInlineImages(t *testing.T) {
	raw, err := Build(Message{
		From: mail.Address{Address: "a@example.com"}, To: "b@example.com", Subject: "x",
		HTML:        `<p>Logo:</p><img src="cid:img1@airletter">`,
		Inline:      []Inline{{ContentID: "img1@airletter", Filename: "logo.png", MimeType: "image/png", Content: []byte("png")}},
		Attachments: []Attachment{{Filename: "a.pdf", MimeType: "application/pdf", Content: []byte("pdf")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}

	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	mixed := multipart.NewReader(msg.Body, params["boundary"])
	body, err := mixed.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	relType, relParams, _ := mime.ParseMediaType(body.Header.Get("Content-Type"))
	if relType != "multipart/related" {
		t.Fatalf("body = %s, want multipart/related", relType)
	}

	related := multipart.NewReader(body, relParams["boundary"])
	alt, _ := related.NextPart()
	if !strings.HasPrefix(alt.Header.Get("Content-Type"), "multipart/alternative") {
		t.Errorf("first related part = %s", alt.Header.Get("Content-Type"))
	}
	img, err := related.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if img.Header.Get("Content-ID") != "<img1@airletter>" || !strings.HasPrefix(img.Header.Get("Content-Disposition"), "inline") {
		t.Errorf("inline headers: %v", img.Header)
	}

	att, err := mixed.NextPart()
	if err != nil || att.FileName() != "a.pdf" {
		t.Errorf("attachment after body: %v %v", att, err)
	}
}

func TestHTMLToTextSkipsHeadAndStyles(t *testing.T) {
	got := htmlToText(`<!DOCTYPE html><html><head><title>T</title><style>p{color:red}</style></head><body><!-- x --><p>Hello</p></body></html>`)
	if got != "Hello" {
		t.Errorf("got %q", got)
	}
}
