package user

import (
	"gorm.io/gorm"
)

// User signs in with an email and a password
type User struct {
	gorm.Model
	Email        string
	PasswordHash string `json:"-"`
	PictureUrl   string
	FirstName    string
	LastName     string
}
