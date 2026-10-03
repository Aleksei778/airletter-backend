package token

import (
	"time"
)

type FindOrCreate struct {
	UserID  uint
	Access  string
	Refresh string
	Expiry  time.Time
	// Google account the tokens belong to
	GoogleSub   string
	GoogleEmail string
}
