package sheets

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"

	tokenmod "airletter/internal/token"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

var ErrNoEmails = errors.New("sheets: no emails found in range")

var spreadsheetURLRe = regexp.MustCompile(`/spreadsheets/d/([a-zA-Z0-9-_]+)`)

type Service struct {
	tokenSvc *tokenmod.Service
}

func NewService(tokenSvc *tokenmod.Service) *Service {
	return &Service{tokenSvc: tokenSvc}
}

// SpreadsheetID accepts either a bare spreadsheet ID or a full Google Sheets URL
func SpreadsheetID(idOrURL string) string {
	idOrURL = strings.TrimSpace(idOrURL)
	if m := spreadsheetURLRe.FindStringSubmatch(idOrURL); m != nil {
		return m[1]
	}
	return idOrURL
}

// ParseEmails reads the first column of the range and returns unique valid emails
func (svc *Service) ParseEmails(ctx context.Context, userID uint, spreadsheetID, rng string) ([]string, error) {
	ts, err := svc.tokenSvc.TokenSource(ctx, userID)
	if err != nil {
		return nil, err
	}

	client, err := sheets.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("sheets: create client: %w", err)
	}

	result, err := client.Spreadsheets.Values.Get(spreadsheetID, rng).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("sheets: get values: %w", err)
	}

	emails := extractEmails(result.Values)
	if len(emails) == 0 {
		return nil, ErrNoEmails
	}

	return emails, nil
}

func extractEmails(values [][]any) []string {
	seen := make(map[string]struct{})
	emails := make([]string, 0, len(values))

	for _, row := range values {
		if len(row) == 0 {
			continue
		}

		cell, ok := row[0].(string)
		if !ok {
			continue
		}

		addr, err := mail.ParseAddress(strings.TrimSpace(cell))
		if err != nil {
			continue
		}

		email := strings.ToLower(addr.Address)
		if _, exists := seen[email]; !exists {
			seen[email] = struct{}{}
			emails = append(emails, email)
		}
	}

	return emails
}
