package gmail

import (
	"context"
	"encoding/base64"
	"fmt"

	tokenmod "airletter/internal/token"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type Service struct {
	tokenSvc *tokenmod.Service
}

func NewService(tokenSvc *tokenmod.Service) *Service {
	return &Service{tokenSvc: tokenSvc}
}

// Send sends a raw RFC 2822 message from the user's mailbox
func (svc *Service) Send(ctx context.Context, userID uint, raw []byte) (*gmail.Message, error) {
	ts, err := svc.tokenSvc.TokenSource(ctx, userID)
	if err != nil {
		return nil, err
	}

	client, err := gmail.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("gmail: create client: %w", err)
	}

	msg := &gmail.Message{Raw: base64.URLEncoding.EncodeToString(raw)}

	return client.Users.Messages.Send("me", msg).Context(ctx).Do()
}
