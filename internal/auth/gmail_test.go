package auth

import (
	"context"
	"errors"
	"net"
	"testing"
)

// fakeMX replaces DNS for the test: tests must not depend on real MX records
// (example.com, for one, has a null MX and is never Gmail)
func fakeMX(t *testing.T) {
	t.Helper()
	lookupMX = func(_ context.Context, domain string) ([]*net.MX, error) {
		switch domain {
		case "workspace.example":
			return []*net.MX{{Host: "ASPMX.L.GOOGLE.COM."}}, nil
		case "new-workspace.example":
			return []*net.MX{{Host: "smtp.google.com."}}, nil
		case "yandex.ru":
			return []*net.MX{{Host: "mx.yandex.ru."}}, nil
		case "flaky.example":
			return nil, &net.DNSError{Err: "timeout", IsTimeout: true}
		}
		return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
	}
	t.Cleanup(func() { lookupMX = net.DefaultResolver.LookupMX })
}

func TestIsGmail(t *testing.T) {
	fakeMX(t)

	for email, want := range map[string]bool{
		"ivan@gmail.com":             true,
		"ivan@GoogleMail.com":        true,
		"ivan@workspace.example":     true,
		"ivan@new-workspace.example": true,
		"ivan@yandex.ru":             false,
		"ivan@missing.example":       false,
		"no-at-sign":                 false,
	} {
		if got, err := IsGmail(context.Background(), email); err != nil || got != want {
			t.Errorf("IsGmail(%q) = %v %v, want %v", email, got, err, want)
		}
	}

	if _, err := IsGmail(context.Background(), "ivan@flaky.example"); err == nil {
		t.Error("IsGmail on a DNS timeout: want an error")
	}
	if err := requireGmail(context.Background(), "ivan@flaky.example"); err != nil {
		t.Errorf("requireGmail on a DNS timeout = %v, want nil (fail open)", err)
	}
	if err := requireGmail(context.Background(), "ivan@yandex.ru"); !errors.Is(err, ErrNotGmail) {
		t.Errorf("requireGmail(yandex) = %v, want ErrNotGmail", err)
	}
}
