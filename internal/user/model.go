package user

import (
	"gorm.io/gorm"
)

// User signs in with an email or a phone number and a password.
// At least one of Email and Phone is set; empty means "not set".
type User struct {
	gorm.Model
	Email        string
	Phone        string
	PasswordHash string `json:"-"`
	PictureUrl   string
	FirstName    string
	LastName     string
}

// Login is the identifier to show: email if set, otherwise phone
func (u *User) Login() string {
	if u.Email != "" {
		return u.Email
	}
	return u.Phone
}
