package token

import (
	"airletter/internal/crypto"
	"airletter/internal/user"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type Token struct {
	gorm.Model
	UserID  uint      `gorm:"uniqueIndex;not null"`
	User    user.User `gorm:"foreignKey:UserID"`
	Access  string    `gorm:"column:access;type:text;not null"`
	Refresh string    `gorm:"column:refresh;type:text;not null"`
	Expiry  time.Time `gorm:"not null"`
	// connected Google account: emails are sent from this address
	GoogleSub   string
	GoogleEmail string
}

func (t *Token) BeforeSave(tx *gorm.DB) (err error) {
	t.Access, err = crypto.Encrypt(t.Access)
	if err != nil {
		return fmt.Errorf("could not encrypt access token: %v", err)
	}

	t.Refresh, err = crypto.Encrypt(t.Refresh)
	if err != nil {
		return fmt.Errorf("could not encrypt refresh token: %v", err)
	}

	return nil
}

func (t *Token) AfterSave(tx *gorm.DB) (err error) {
	t.Access, err = crypto.Decrypt(t.Access)
	if err != nil {
		return fmt.Errorf("could not decrypt access token after save: %v", err)
	}

	t.Refresh, err = crypto.Decrypt(t.Refresh)
	if err != nil {
		return fmt.Errorf("could not decrypt refresh token after save: %v", err)
	}

	return nil
}

func (t *Token) AfterFind(tx *gorm.DB) (err error) {
	t.Access, err = crypto.Decrypt(t.Access)
	if err != nil {
		return fmt.Errorf("could not decrypt access token: %v", err)
	}

	t.Refresh, err = crypto.Decrypt(t.Refresh)
	if err != nil {
		return fmt.Errorf("could not decrypt refresh token: %v", err)
	}

	return nil
}

func (t *Token) IsExpired() bool {
	return time.Now().After(t.Expiry)
}
