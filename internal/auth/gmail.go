package auth

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"time"
)

// ErrNotGmail means the address is not a Gmail mailbox, so Airletter cannot send from it
var ErrNotGmail = errors.New("auth: email is not served by Gmail")

// lookupMX is replaced in tests
var lookupMX = net.DefaultResolver.LookupMX

// IsGmail reports whether mail for the address is handled by Gmail: gmail.com
// itself or a Google Workspace domain whose MX records point to Google.
// A DNS failure other than "no such domain" is returned as an error.
func IsGmail(ctx context.Context, email string) (bool, error) {
	_, domain, ok := strings.Cut(strings.ToLower(email), "@")
	if !ok {
		return false, nil
	}
	if domain == "gmail.com" || domain == "googlemail.com" {
		return true, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	records, err := lookupMX(ctx, domain)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, mx := range records {
		host := strings.TrimSuffix(strings.ToLower(mx.Host), ".")
		if host == "smtp.google.com" || strings.HasSuffix(host, ".google.com") || strings.HasSuffix(host, ".googlemail.com") {
			return true, nil
		}
	}
	return false, nil
}

// requireGmail fails open on DNS errors: a resolver hiccup must not block the user
func requireGmail(ctx context.Context, email string) error {
	ok, err := IsGmail(ctx, email)
	if err != nil {
		slog.Warn("auth: mx lookup", "err", err, "email", email)
		return nil
	}
	if !ok {
		return ErrNotGmail
	}
	return nil
}
