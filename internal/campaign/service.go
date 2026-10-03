package campaign

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"quicksend/internal/config"
	"quicksend/internal/subscription"
	"quicksend/internal/user"
)

var (
	ErrNoSubscription = errors.New("campaign: no active subscription")
	// ErrGmailNotConnected means the user has not granted sending via Gmail
	ErrGmailNotConnected = errors.New("campaign: gmail is not connected")
)

// GrantChecker tells whether the user has connected Gmail
type GrantChecker interface {
	HasRefreshToken(userID uint) (bool, error)
}

// ValidationError is returned for invalid user input
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

type AttachmentInput struct {
	Filename string
	MimeType string
	Content  []byte
	// set for inline images referenced from the HTML body as cid:<ContentID>
	ContentID string
}

// contentIDRe keeps Content-ID values safe to put into a MIME header
var contentIDRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,100}$`)

type CreateInput struct {
	Subject string
	Body    string
	// FormatHTML (default) or FormatText
	Format      BodyFormat
	Recipients  []string
	Attachments []AttachmentInput
	// nil or a time in the past means "send now"
	ScheduledAt *time.Time
}

type CreateResult struct {
	Campaign *Campaign
	// Skipped are recipients that are not valid email addresses
	Skipped []string
}

// Dispatcher triggers releasing pending recipients right away instead of
// waiting for the next periodic run
type Dispatcher interface {
	TriggerDispatch(ctx context.Context) error
}

type Service struct {
	repo       *Repository
	subs       *subscription.Service
	grants     GrantChecker
	cfg        *config.Config
	dispatcher Dispatcher
}

func NewService(repo *Repository, subs *subscription.Service, grants GrantChecker, cfg *config.Config, d Dispatcher) *Service {
	return &Service{repo: repo, subs: subs, grants: grants, cfg: cfg, dispatcher: d}
}

func (s *Service) Create(ctx context.Context, u *user.User, in CreateInput) (*CreateResult, error) {
	sub, err := s.subs.Active(u.ID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, ErrNoSubscription
	}

	connected, err := s.grants.HasRefreshToken(u.ID)
	if err != nil {
		return nil, err
	}
	if !connected {
		return nil, ErrGmailNotConnected
	}

	subject := strings.TrimSpace(in.Subject)
	if subject == "" {
		return nil, invalid("subject is required")
	}
	if strings.TrimSpace(in.Body) == "" {
		return nil, invalid("body is required")
	}
	format := in.Format
	if format == "" {
		format = FormatHTML
	}
	if format != FormatHTML && format != FormatText {
		return nil, invalid("format must be html or text")
	}

	emails, skipped := NormalizeRecipients(in.Recipients)
	if len(emails) == 0 {
		return nil, invalid("no valid recipients")
	}
	if len(emails) > s.cfg.MaxRecipientsPerCampaign {
		return nil, invalid("too many recipients: %d, max %d", len(emails), s.cfg.MaxRecipientsPerCampaign)
	}

	var total int64
	attachments := make([]Attachment, 0, len(in.Attachments))
	for _, a := range in.Attachments {
		if a.Filename == "" || len(a.Content) == 0 {
			return nil, invalid("attachment must have a filename and content")
		}
		if a.ContentID != "" && !contentIDRe.MatchString(a.ContentID) {
			return nil, invalid("invalid content_id for %s", a.Filename)
		}
		total += int64(len(a.Content))
		attachments = append(attachments, Attachment{
			Filename:  a.Filename,
			MimeType:  a.MimeType,
			ContentID: a.ContentID,
			Size:      int64(len(a.Content)),
			Content:   a.Content,
		})
	}
	if total > s.cfg.MaxAttachmentsBytes {
		return nil, invalid("attachments are too large: %d bytes, max %d", total, s.cfg.MaxAttachmentsBytes)
	}

	now := time.Now().UTC()
	scheduledAt := now
	if in.ScheduledAt != nil && in.ScheduledAt.After(now) {
		scheduledAt = in.ScheduledAt.UTC()
	}

	recipients := make([]Recipient, len(emails))
	for i, e := range emails {
		recipients[i] = Recipient{Email: e, Status: RecipientPending}
	}

	c := &Campaign{
		UserID:      u.ID,
		SenderName:  strings.TrimSpace(u.FirstName + " " + u.LastName),
		Subject:     subject,
		Body:        in.Body,
		BodyFormat:  format,
		Status:      StatusScheduled,
		ScheduledAt: scheduledAt,
		Recipients:  recipients,
		Attachments: attachments,
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, fmt.Errorf("campaign: create: %w", err)
	}

	if scheduledAt.Equal(now) && s.dispatcher != nil {
		// not critical: the periodic dispatch picks it up anyway
		_ = s.dispatcher.TriggerDispatch(ctx)
	}

	return &CreateResult{Campaign: c, Skipped: skipped}, nil
}

// NormalizeRecipients lowercases, validates and de-duplicates emails.
// Invalid entries are returned separately.
func NormalizeRecipients(raw []string) (valid, skipped []string) {
	seen := make(map[string]struct{}, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		addr, err := mail.ParseAddress(r)
		if err != nil || !strings.Contains(addr.Address, ".") {
			skipped = append(skipped, r)
			continue
		}
		email := strings.ToLower(addr.Address)
		if _, dup := seen[email]; dup {
			continue
		}
		seen[email] = struct{}{}
		valid = append(valid, email)
	}
	return valid, skipped
}

// ParseSchedule builds a UTC time from the extension's date ("2006-01-02"),
// time ("15:04") and IANA timezone. Empty date and time mean "send now".
func ParseSchedule(date, clock, tz string) (*time.Time, error) {
	if date == "" && clock == "" {
		return nil, nil
	}
	if date == "" || clock == "" {
		return nil, invalid("both date and time are required for scheduling")
	}

	loc := time.UTC
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return nil, invalid("unknown timezone %q", tz)
		}
		loc = l
	}

	t, err := time.ParseInLocation("2006-01-02 15:04", date+" "+clock, loc)
	if err != nil {
		return nil, invalid("invalid date or time")
	}
	t = t.UTC()
	return &t, nil
}

type WithStats struct {
	Campaign
	Stats Stats
}

func (s *Service) List(ctx context.Context, userID uint, limit, offset int) ([]WithStats, error) {
	campaigns, err := s.repo.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	return s.withStats(ctx, campaigns)
}

func (s *Service) Get(ctx context.Context, id, userID uint) (*WithStats, error) {
	c, err := s.repo.FindForUser(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	res, err := s.withStats(ctx, []Campaign{*c})
	if err != nil {
		return nil, err
	}
	return &res[0], nil
}

func (s *Service) Cancel(ctx context.Context, id, userID uint) error {
	return s.repo.Cancel(ctx, id, userID)
}

func (s *Service) Totals(ctx context.Context, userID uint) (*UserTotals, error) {
	return s.repo.TotalsByUser(ctx, userID)
}

func (s *Service) withStats(ctx context.Context, campaigns []Campaign) ([]WithStats, error) {
	ids := make([]uint, len(campaigns))
	for i, c := range campaigns {
		ids[i] = c.ID
	}
	stats, err := s.repo.StatsByCampaign(ctx, ids)
	if err != nil {
		return nil, err
	}

	res := make([]WithStats, len(campaigns))
	for i, c := range campaigns {
		res[i] = WithStats{Campaign: c, Stats: *stats[c.ID]}
	}
	return res, nil
}
