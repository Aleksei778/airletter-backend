package mailer

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
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

type Message struct {
	From        mail.Address
	To          string
	Subject     string
	HTML        string
	Attachments []Attachment
}

// Build renders the message as RFC 2822 bytes ready for Gmail API messages.send
func Build(m Message) ([]byte, error) {
	var buf bytes.Buffer

	writeHeader(&buf, "From", m.From.String())
	writeHeader(&buf, "To", m.To)
	writeHeader(&buf, "Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	writeHeader(&buf, "Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", newMessageID(m.From.Address))
	writeHeader(&buf, "MIME-Version", "1.0")

	altType, altBody, err := renderAlternative(m.HTML)
	if err != nil {
		return nil, err
	}

	if len(m.Attachments) == 0 {
		writeHeader(&buf, "Content-Type", altType)
		buf.WriteString("\r\n")
		buf.Write(altBody)
		return buf.Bytes(), nil
	}

	mixed := multipart.NewWriter(&buf)
	writeHeader(&buf, "Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixed.Boundary()}))
	buf.WriteString("\r\n")

	bodyPart, err := mixed.CreatePart(textproto.MIMEHeader{"Content-Type": {altType}})
	if err != nil {
		return nil, err
	}
	if _, err := bodyPart.Write(altBody); err != nil {
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

// renderAlternative renders multipart/alternative (plain text + html) and returns its content type and body
func renderAlternative(htmlBody string) (string, []byte, error) {
	var buf bytes.Buffer
	alt := multipart.NewWriter(&buf)

	parts := []struct {
		contentType string
		content     string
	}{
		{"text/plain; charset=utf-8", htmlToText(htmlBody)},
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
		qp := quotedprintable.NewWriter(w)
		if _, err := qp.Write([]byte(p.content)); err != nil {
			return "", nil, err
		}
		if err := qp.Close(); err != nil {
			return "", nil, err
		}
	}

	if err := alt.Close(); err != nil {
		return "", nil, err
	}

	contentType := mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": alt.Boundary()})
	return contentType, buf.Bytes(), nil
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

	encoded := base64.StdEncoding.EncodeToString(a.Content)
	for len(encoded) > 76 {
		if _, err := w.Write([]byte(encoded[:76] + "\r\n")); err != nil {
			return err
		}
		encoded = encoded[76:]
	}
	_, err = w.Write([]byte(encoded + "\r\n"))
	return err
}

func writeHeader(buf *bytes.Buffer, key, value string) {
	// strip CR/LF to prevent header injection from user-provided values
	value = strings.NewReplacer("\r", "", "\n", "").Replace(value)
	fmt.Fprintf(buf, "%s: %s\r\n", key, value)
}

func newMessageID(from string) string {
	domain := "quicksend.local"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		domain = from[i+1:]
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("<%x.%d@%s>", b, time.Now().UnixNano(), domain)
}

var (
	blockTagRe = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li|/h[1-6]|/tr)\s*/?>`)
	tagRe      = regexp.MustCompile(`<[^>]*>`)
	spacesRe   = regexp.MustCompile(`[ \t]+`)
	newlinesRe = regexp.MustCompile(`\n{3,}`)
)

// htmlToText produces a rough plain-text version; having one improves deliverability
func htmlToText(s string) string {
	s = blockTagRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = spacesRe.ReplaceAllString(s, " ")
	s = newlinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
