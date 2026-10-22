package campaign

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidToken = errors.New("campaign: invalid unsubscribe token")

// token = recipient ID (8 bytes) + truncated HMAC of it, base64url
const unsubscribeMACSize = 16

// UnsubscribeToken identifies a recipient in unsubscribe links without
// exposing the email or letting anyone guess tokens of other recipients
func UnsubscribeToken(key []byte, recipientID uint) string {
	b := binary.BigEndian.AppendUint64(nil, uint64(recipientID))
	b = append(b, unsubscribeMAC(key, b)...)
	return base64.RawURLEncoding.EncodeToString(b)
}

func ParseUnsubscribeToken(key []byte, token string) (uint, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 8+unsubscribeMACSize {
		return 0, ErrInvalidToken
	}
	if !hmac.Equal(b[8:], unsubscribeMAC(key, b[:8])) {
		return 0, ErrInvalidToken
	}
	return uint(binary.BigEndian.Uint64(b[:8])), nil
}

func unsubscribeMAC(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)[:unsubscribeMACSize]
}

// ---- repository ----

// Unsubscribe puts the recipient's address on the sender's stop list and
// drops emails to it that are not sent yet, in this and other campaigns
func (r *Repository) Unsubscribe(ctx context.Context, recipientID uint) (*Recipient, error) {
	var rec Recipient
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rec, recipientID).Error; err != nil {
			return err
		}

		u := Unsubscribe{UserID: rec.UserID, Email: rec.Email, CampaignID: &rec.CampaignID, CreatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&u).Error; err != nil {
			return err
		}

		// a recipient in "sending" is checked by the sender right before the send
		return tx.Model(&Recipient{}).
			Where("user_id = ? AND email = ? AND status IN ?", rec.UserID, rec.Email, []RecipientStatus{RecipientPending, RecipientQueued}).
			Update("status", RecipientUnsubscribed).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &rec, err
}

func (r *Repository) FindByID(ctx context.Context, id uint) (*Campaign, error) {
	var c Campaign
	err := r.db.WithContext(ctx).First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &c, err
}

func (r *Repository) IsUnsubscribed(ctx context.Context, userID uint, email string) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Unsubscribe{}).
		Where("user_id = ? AND email = ?", userID, email).
		Count(&n).Error
	return n > 0, err
}

// UnsubscribedAmong returns which of the emails are on the user's stop list
func (r *Repository) UnsubscribedAmong(ctx context.Context, userID uint, emails []string) (map[string]struct{}, error) {
	res := make(map[string]struct{})
	for chunk := range slices.Chunk(emails, 1000) {
		var found []string
		err := r.db.WithContext(ctx).Model(&Unsubscribe{}).
			Where("user_id = ? AND email IN ?", userID, chunk).
			Pluck("email", &found).Error
		if err != nil {
			return nil, err
		}
		for _, e := range found {
			res[e] = struct{}{}
		}
	}
	return res, nil
}

// ---- service ----

type UnsubscribeInfo struct {
	Email  string
	Sender string
	// already on the sender's stop list
	Unsubscribed bool
}

// UnsubscribeInfo is shown on the unsubscribe page before the recipient confirms
func (s *Service) UnsubscribeInfo(ctx context.Context, key []byte, token string) (*UnsubscribeInfo, error) {
	id, err := ParseUnsubscribeToken(key, token)
	if err != nil {
		return nil, err
	}
	rec, err := s.repo.FindRecipient(ctx, id)
	if err != nil {
		return nil, err
	}
	c, err := s.repo.FindByID(ctx, rec.CampaignID)
	if err != nil {
		return nil, err
	}
	done, err := s.repo.IsUnsubscribed(ctx, rec.UserID, rec.Email)
	if err != nil {
		return nil, err
	}
	return &UnsubscribeInfo{Email: rec.Email, Sender: c.SenderName, Unsubscribed: done}, nil
}

func (s *Service) Unsubscribe(ctx context.Context, key []byte, token string) error {
	id, err := ParseUnsubscribeToken(key, token)
	if err != nil {
		return err
	}
	_, err = s.repo.Unsubscribe(ctx, id)
	return err
}
