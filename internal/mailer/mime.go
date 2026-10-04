package mailer

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"time"
)

type Attachment struct {
	Filename string
	MimeType string
	Content  []byte
}

// Inline is an image shown inside the HTML body as <img src="cid:ContentID">
type Inline struct {
	ContentID string
	Filename  string
	MimeType  string
	Content   []byte
}

type Message struct {
	From    mail.Address
	To      string
	Subject string
	// HTML body; when empty the message is plain text (Text)
	HTML string
	// Text body for plain-text messages; for HTML it is derived when empty
	Text        string
	Inline      []Inline
	Attachments []Attachment
}

// Build renders the message as RFC 2822 bytes ready for Gmail API messages.send.
//
//	mixed                      (only with attachments)
//	├─ related                 (only with inline images)
//	│  ├─ alternative: text/plain + text/html
//	│  └─ inline images
//	└─ attachments
func Build(m Message) ([]byte, error) {
	var buf bytes.Buffer

	writeHeader(&buf, "From", m.From.String())
	writeHeader(&buf, "To", m.To)
	writeHeader(&buf, "Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	writeHeader(&buf, "Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", newMessageID(m.From.Address))
	writeHeader(&buf, "MIME-Version", "1.0")

	bodyType, body, err := renderBody(m)
	if err != nil {
		return nil, err
	}

	if len(m.Attachments) == 0 {
		writeHeader(&buf, "Content-Type", bodyType)
		buf.WriteString("\r\n")
		buf.Write(body)
		return buf.Bytes(), nil
	}

	mixed := multipart.NewWriter(&buf)
	writeHeader(&buf, "Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixed.Boundary()}))
	buf.WriteString("\r\n")

	bodyPart, err := mixed.CreatePart(textproto.MIMEHeader{"Content-Type": {bodyType}})
	if err != nil {
		return nil, err
	}
	if _, err := bodyPart.Write(body); err != nil {
		return nil, err
	}

	for _, a := range m.Attachments {
		if err := writeAttachment(mixed, a); err != nil {
			return nil, err
		}
	}

	if err := mixed.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// renderBody returns the content type and bytes of everything but attachments
func renderBody(m Message) (string, []byte, error) {
	if m.HTML == "" {
		var buf bytes.Buffer
		if err := writeQP(&buf, m.Text); err != nil {
			return "", nil, err
		}
		return "text/plain; charset=utf-8", buf.Bytes(), nil
	}

	text := m.Text
	if text == "" {
		text = htmlToText(m.HTML)
	}
	altType, alt, err := renderAlternative(text, m.HTML)
	if err != nil || len(m.Inline) == 0 {
		return altType, alt, err
	}

	var buf bytes.Buffer
	related := multipart.NewWriter(&buf)
	part, err := related.CreatePart(textproto.MIMEHeader{"Content-Type": {altType}})
	if err != nil {
		return "", nil, err
	}
	if _, err := part.Write(alt); err != nil {
		return "", nil, err
	}
	for _, img := range m.Inline {
		if err := writeInline(related, img); err != nil {
			return "", nil, err
		}
	}
	if err := related.Close(); err != nil {
		return "", nil, err
	}

	relType := mime.FormatMediaType("multipart/related", map[string]string{"boundary": related.Boundary(), "type": "multipart/alternative"})
	return relType, buf.Bytes(), nil
}

// renderAlternative renders multipart/alternative (plain text + html) and returns its content type and body
func renderAlternative(text, htmlBody string) (string, []byte, error) {
	var buf bytes.Buffer
	alt := multipart.NewWriter(&buf)

	parts := []struct {
		contentType string
		content     string
	}{
		{"text/plain; charset=utf-8", text},
		{"text/html; charset=utf-8", htmlBody},
	}

	for _, p := range parts {
		w, err := alt.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {p.contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return "", nil, err
		}
		if err := writeQP(w, p.content); err != nil {
			return "", nil, err
		}
	}

	if err := alt.Close(); err != nil {
		return "", nil, err
	}

	contentType := mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": alt.Boundary()})
	return contentType, buf.Bytes(), nil
}

func writeQP(w io.Writer, content string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(content)); err != nil {
		return err
	}
	return qp.Close()
}

func writeInline(mw *multipart.Writer, img Inline) error {
	mimeType := img.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {mime.FormatMediaType(mimeType, map[string]string{"name": img.Filename})},
		"Content-Disposition":       {mime.FormatMediaType("inline", map[string]string{"filename": img.Filename})},
		"Content-ID":                {"<" + img.ContentID + ">"},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}
	return writeBase64(w, img.Content)
}

func writeAttachment(mw *multipart.Writer, a Attachment) error {
	mimeType := a.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	w, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {mime.FormatMediaType(mimeType, map[string]string{"name": a.Filename})},
		"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename})},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}

	return writeBase64(w, a.Content)
}

// writeBase64 writes base64 in 76-character lines (RFC 2045)
func writeBase64(w io.Writer, content []byte) error {
	encoded := base64.StdEncoding.EncodeToString(content)
	for len(encoded) > 76 {
		if _, err := io.WriteString(w, encoded[:76]+"\r\n"); err != nil {
			return err
		}
		encoded = encoded[76:]
	}
	_, err := io.WriteString(w, encoded+"\r\n")
	return err
}

func writeHeader(buf *bytes.Buffer, key, value string) {
	// strip CR/LF to prevent header injection from user-provided values
	value = strings.NewReplacer("\r", "", "\n", "").Replace(value)
	fmt.Fprintf(buf, "%s: %s\r\n", key, value)
}

func newMessageID(from string) string {
	domain := "airletter.local"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		domain = from[i+1:]
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("<%x.%d@%s>", b, time.Now().UnixNano(), domain)
}

var (
	// content of these elements is never readable text (CSS, scripts, <head> of HTML templates)
	hiddenBlocksRe = regexp.MustCompile(`(?is)<(head|style|script|title)\b[^>]*>.*?</(head|style|script|title)>`)
	commentRe      = regexp.MustCompile(`(?s)<!--.*?-->`)
	blockTagRe     = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li|/h[1-6]|/tr)\s*/?>`)
	tagRe          = regexp.MustCompile(`<[^>]*>`)
	spacesRe       = regexp.MustCompile(`[ \t]+`)
	newlinesRe     = regexp.MustCompile(`\n{3,}`)
)

// htmlToText produces a rough plain-text version; having one improves deliverability
func htmlToText(s string) string {
	s = hiddenBlocksRe.ReplaceAllString(s, "")
	s = commentRe.ReplaceAllString(s, "")
	s = blockTagRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = spacesRe.ReplaceAllString(s, " ")
	s = newlinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
