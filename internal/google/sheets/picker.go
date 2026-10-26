package sheets

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// With the drive.file scope Airletter reads only spreadsheets the user picked
// in Google Picker. The Picker runs on the website, which the extension opens
// with a one-time code instead of a Google token in the URL.

const pickerCodeTTL = 5 * time.Minute

// ErrInvalidPickerCode means the code is unknown, expired or already used
var ErrInvalidPickerCode = errors.New("sheets: invalid picker code")

// ErrPickerNotConfigured means the Picker API key or project number is missing
var ErrPickerNotConfigured = errors.New("sheets: google picker is not configured")

type PickerConfig struct {
	APIKey string
	// Cloud project number, the Picker's app ID
	AppID string
}

// PickerSession is what the website needs to show the Picker for the user
type PickerSession struct {
	AccessToken string
	APIKey      string
	AppID       string
}

func pickerCodeKey(code string) string { return "sheets:picker_code:" + code }

// NewPickerCode creates a one-time code the website exchanges for a PickerSession
func (svc *Service) NewPickerCode(ctx context.Context, userID uint) (string, error) {
	if svc.picker.AppID == "" {
		return "", ErrPickerNotConfigured
	}
	code := rand.Text()
	if err := svc.rdb.Set(ctx, pickerCodeKey(code), userID, pickerCodeTTL).Err(); err != nil {
		return "", fmt.Errorf("sheets: save picker code: %w", err)
	}
	return code, nil
}

// PickerSession consumes the code and returns a fresh access token of its user.
// The token never leaves our origin: the website passes it only to the Picker.
func (svc *Service) PickerSession(ctx context.Context, code string) (*PickerSession, error) {
	userID, err := svc.rdb.GetDel(ctx, pickerCodeKey(code)).Uint64()
	if errors.Is(err, redis.Nil) {
		return nil, ErrInvalidPickerCode
	}
	if err != nil {
		return nil, fmt.Errorf("sheets: consume picker code: %w", err)
	}

	ts, err := svc.tokenSvc.TokenSource(ctx, uint(userID))
	if err != nil {
		return nil, err
	}
	t, err := ts.Token()
	if err != nil {
		return nil, err
	}
	return &PickerSession{AccessToken: t.AccessToken, APIKey: svc.picker.APIKey, AppID: svc.picker.AppID}, nil
}
