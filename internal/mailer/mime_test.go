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
