package auth

import (
	"errors"
	"net/mail"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidLogin       = errors.New("auth: login must be an email or a phone number")
	ErrWeakPassword       = errors.New("auth: password must be 8 to 72 characters")
	ErrInvalidCredentials = errors.New("auth: wrong login or password")
	ErrTooManyAttempts    = errors.New("auth: too many failed attempts")
)

type LoginKind int

const (
	LoginEmail LoginKind = iota + 1
	LoginPhone
)

var phoneJunk = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "")
var phoneRe = regexp.MustCompile(`^\+[1-9][0-9]{9,14}$`)

// ParseLogin normalizes an email (lowercase) or a phone number (E.164,
// Russian 8XXXXXXXXXX and 7XXXXXXXXXX become +7XXXXXXXXXX).
func ParseLogin(raw string) (LoginKind, string, error) {
	raw = strings.TrimSpace(raw)

	if strings.Contains(raw, "@") {
		addr, err := mail.ParseAddress(raw)
		if err != nil || addr.Address != raw || !strings.Contains(addr.Address[strings.LastIndex(addr.Address, "@"):], ".") {
			return 0, "", ErrInvalidLogin
		}
		return LoginEmail, strings.ToLower(addr.Address), nil
	}

	phone := phoneJunk.Replace(raw)
	switch {
	case len(phone) == 11 && (phone[0] == '8' || phone[0] == '7'):
		phone = "+7" + phone[1:]
	case !strings.HasPrefix(phone, "+"):
		phone = "+" + phone
	}
	if !phoneRe.MatchString(phone) {
		return 0, "", ErrInvalidLogin
	}
	return LoginPhone, phone, nil
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
