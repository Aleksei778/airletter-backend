package auth

import (
	"errors"
	"net/mail"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidEmail       = errors.New("auth: invalid email")
	ErrWeakPassword       = errors.New("auth: password must be 8 to 72 characters")
	ErrInvalidCredentials = errors.New("auth: wrong email or password")
	ErrTooManyAttempts    = errors.New("auth: too many failed attempts")
)

// ParseEmail accepts a bare address with a dotted domain and lowercases it
func ParseEmail(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Address != raw || !strings.Contains(raw[strings.LastIndex(raw, "@"):], ".") {
		return "", ErrInvalidEmail
	}
	return strings.ToLower(raw), nil
}

const bcryptCost = 12

func HashPassword(password string) (string, error) {
	// bcrypt only uses the first 72 bytes: refuse longer passwords instead of truncating
	if len(password) < 8 || len(password) > 72 {
		return "", ErrWeakPassword
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(h), err
}

// dummyHash is compared against when the login does not exist, so the
// response time does not reveal which logins are registered
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("airletter-dummy-password"), bcryptCost)

func checkPassword(hash, password string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
