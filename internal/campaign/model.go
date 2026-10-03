package campaign

import (
	"time"

	"quicksend/internal/user"

	"gorm.io/gorm"
)

type Status string

const (
	StatusScheduled Status = "scheduled"
	StatusSending   Status = "sending"
	StatusCompleted Status = "completed"
	StatusPaused    Status = "paused"
	StatusCancelled Status = "cancelled"
)

type RecipientStatus string

const (
	RecipientPending   RecipientStatus = "pending"
	RecipientQueued    RecipientStatus = "queued"
	RecipientSending   RecipientStatus = "sending"
	RecipientSent      RecipientStatus = "sent"
	RecipientFailed    RecipientStatus = "failed"
	RecipientCancelled RecipientStatus = "cancelled"
)

// Pause reasons shown to the user
const (
	PauseReauthRequired = "reauth_required"
	PauseNoSubscription = "no_subscription"
)

type Campaign struct {
	gorm.Model
	UserID      uint      `gorm:"not null;index"`
	User        user.User `gorm:"foreignKey:UserID"`
	SenderName  string
	Subject     string    `gorm:"not null"`
	Body        string    `gorm:"type:text;not null"` // HTML
	Status      Status    `gorm:"type:varchar(16);not null;index"`
	PauseReason string    `gorm:"type:varchar(32)"`
	ScheduledAt time.Time `gorm:"not null;index"`
	StartedAt   *time.Time
	FinishedAt  *time.Time
	Recipients  []Recipient  `gorm:"foreignKey:CampaignID"`
	Attachments []Attachment `gorm:"foreignKey:CampaignID"`
}

type Recipient struct {
	ID             uint            `gorm:"primaryKey"`
	CampaignID     uint            `gorm:"not null;uniqueIndex:idx_recipient_campaign_email;index:idx_recipient_campaign_status"`
	UserID         uint            `gorm:"not null;index:idx_recipient_user_status"`
	Email          string          `gorm:"not null;uniqueIndex:idx_recipient_campaign_email"`
	Status         RecipientStatus `gorm:"type:varchar(16);not null;index:idx_recipient_campaign_status;index:idx_recipient_user_status"`
	Attempts       int             `gorm:"not null;default:0"`
	Error          string          `gorm:"type:text"`
	GmailMessageID string
	GmailThreadID  string
	SentAt         *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Attachment struct {
	ID         uint   `gorm:"primaryKey"`
	CampaignID uint   `gorm:"not null;index"`
	Filename   string `gorm:"not null"`
	MimeType   string `gorm:"not null;default:'application/octet-stream'"`
	Size       int64  `gorm:"not null"`
	Content    []byte `gorm:"type:bytea;not null"`
	CreatedAt  time.Time
}

// Stats is a per-status count of recipients
type Stats struct {
	Total   int64 `json:"total"`
	Pending int64 `json:"pending"`
	Sent    int64 `json:"sent"`
	Failed  int64 `json:"failed"`
}
