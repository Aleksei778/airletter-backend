package sheets

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	tokenmod "airletter/internal/token"

	"github.com/redis/go-redis/v9"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

var ErrNoEmails = errors.New("sheets: no emails found in range")

// ErrFileAccessRequired means the user has not picked the spreadsheet in
// Google Picker yet (or it does not exist): drive.file sees only picked files
var ErrFileAccessRequired = errors.New("sheets: spreadsheet was not picked")

// ErrInvalidRange means Google rejected the range, e.g. a sheet name typo
var ErrInvalidRange = errors.New("sheets: invalid range")

var spreadsheetURLRe = regexp.MustCompile(`/spreadsheets/d/([a-zA-Z0-9-_]+)`)

type Service struct {
	tokenSvc *tokenmod.Service
	rdb      *redis.Client
	picker   PickerConfig
}

func NewService(tokenSvc *tokenmod.Service, rdb *redis.Client, picker PickerConfig) *Service {
	return &Service{tokenSvc: tokenSvc, rdb: rdb, picker: picker}
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
		return nil, classify(err)
	}

	emails := extractEmails(result.Values)
	if len(emails) == 0 {
		return nil, ErrNoEmails
	}

	return emails, nil
}

// classify maps Google API errors to what the user can fix
func classify(err error) error {
	var gErr *googleapi.Error
	if !errors.As(err, &gErr) {
		return fmt.Errorf("sheets: get values: %w", err)
	}
	switch {
	// the grant has no drive.file (unchecked on the consent screen):
	// signing in again asks for it
	case gErr.Code == http.StatusForbidden && insufficientScope(gErr):
		return fmt.Errorf("sheets: get values: %w: %w", tokenmod.ErrNoToken, err)
	// our misconfiguration, not the user's: picking the file again won't help
	case gErr.Code == http.StatusForbidden && serviceDisabled(gErr):
		return fmt.Errorf("sheets: get values: sheets api is disabled in the cloud project: %w", err)
	// drive.file answers 404 for files the user has not picked and 403 for
	// picked files the user can no longer open
	case gErr.Code == http.StatusNotFound || gErr.Code == http.StatusForbidden:
		return fmt.Errorf("%w: %w", ErrFileAccessRequired, err)
	case gErr.Code == http.StatusBadRequest:
		return fmt.Errorf("%w: %w", ErrInvalidRange, err)
	}
	return fmt.Errorf("sheets: get values: %w", err)
}

func insufficientScope(e *googleapi.Error) bool {
	for _, item := range e.Errors {
		if item.Reason == "insufficientPermissions" {
			return true
		}
	}
	return strings.Contains(e.Message, "insufficient authentication scopes")
}

func serviceDisabled(e *googleapi.Error) bool {
	for _, item := range e.Errors {
		if item.Reason == "accessNotConfigured" {
			return true
		}
	}
	return strings.Contains(e.Message, "SERVICE_DISABLED") || strings.Contains(e.Message, "has not been used in project")
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
