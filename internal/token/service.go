package token

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"airletter/internal/config"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"gorm.io/gorm"
)

// ErrNoToken means the user never granted offline access (or it was removed)
var ErrNoToken = errors.New("token: google token not found")

// ErrGoogleAccountTaken means the Google account is bound to another user
var ErrGoogleAccountTaken = errors.New("token: google account is bound to another user")

type Service struct {
	db   *gorm.DB
	cfg  *config.Config
	repo *Repository
}

func NewService(db *gorm.DB, repo *Repository, cfg *config.Config) *Service {
	return &Service{db: db, repo: repo, cfg: cfg}
}

// IsReauthRequired reports whether the error means the user has to log in again:
// refresh token revoked/expired or never stored.
func IsReauthRequired(err error) bool {
	if errors.Is(err, ErrNoToken) {
		return true
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return re.ErrorCode == "invalid_grant" || re.ErrorCode == "unauthorized_client"
	}
	return false
}

// TokenSource returns an auto-refreshing token source for the user.
// Refreshed access tokens are persisted; the refresh token is kept unless Google rotates it.
func (svc *Service) TokenSource(ctx context.Context, userID uint) (oauth2.TokenSource, error) {
	t, err := svc.repo.FindByUserID(userID)
	if err != nil {
		return nil, fmt.Errorf("token: find: %w", err)
	}
	if t == nil || t.Refresh == "" {
		return nil, ErrNoToken
	}

	initial := &oauth2.Token{AccessToken: t.Access, RefreshToken: t.Refresh, Expiry: t.Expiry}
	oauthCfg := &oauth2.Config{
		ClientID:     svc.cfg.GoogleClientID,
		ClientSecret: svc.cfg.GoogleClientSecret,
		Endpoint:     google.Endpoint,
	}

	return &persistingSource{
		base: oauth2.ReuseTokenSource(initial, oauthCfg.TokenSource(ctx, initial)),
		svc:  svc,
		tok:  t,
	}, nil
}

type persistingSource struct {
	mu   sync.Mutex
	base oauth2.TokenSource
	svc  *Service
	tok  *Token
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	t, err := p.base.Token()
	if err != nil {
		return nil, err
	}

	if t.AccessToken != p.tok.Access {
		p.tok.Access = t.AccessToken
		p.tok.Expiry = t.Expiry
		if t.RefreshToken != "" {
			p.tok.Refresh = t.RefreshToken
		}
		if err := p.svc.db.Save(p.tok).Error; err != nil {
			return nil, fmt.Errorf("token: save refreshed token: %w", err)
		}
	}

	return t, nil
}

// Upsert stores tokens received on login. An empty refresh token does not
// overwrite the stored one: Google returns it only on the first consent.
func (svc *Service) Upsert(dto FindOrCreate) (*Token, error) {
	t, err := svc.repo.FindByUserID(dto.UserID)
	if err != nil {
		return nil, err
	}

	if t == nil {
		t = &Token{UserID: dto.UserID}
	}

	t.Access = dto.Access
	t.Expiry = dto.Expiry
	if dto.Refresh != "" {
		t.Refresh = dto.Refresh
	}
	if dto.GoogleSub != "" {
		t.GoogleSub = dto.GoogleSub
		t.GoogleEmail = dto.GoogleEmail
	}

	if err := svc.db.Save(t).Error; err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "idx_tokens_google_sub" {
			return nil, ErrGoogleAccountTaken
		}
		return nil, err
	}

	return t, nil
}

// GoogleAccountTaken reports whether the Google account is bound to a user
// other than userID
func (svc *Service) GoogleAccountTaken(sub string, userID uint) (bool, error) {
	t, err := svc.repo.FindByGoogleSub(sub)
	if err != nil {
		return false, err
	}
	return t != nil && t.UserID != userID, nil
}

// HasRefreshToken reports whether offline access is already stored for the user
func (svc *Service) HasRefreshToken(userID uint) (bool, error) {
	t, err := svc.repo.FindByUserID(userID)
	if err != nil {
		return false, err
	}
	return t != nil && t.Refresh != "", nil
}

// Invalidate forgets the stored Google grant after it was revoked, so the next
// login asks for consent again and Google issues a new refresh token.
func (svc *Service) Invalidate(userID uint) error {
	t, err := svc.repo.FindByUserID(userID)
	if err != nil || t == nil {
		return err
	}
	t.Access = ""
	t.Refresh = ""
	return svc.db.Save(t).Error
}

// Account describes the connected Google account
type Account struct {
	Connected bool
	Email     string
	// Sub is the Google account bound to the user on the first connect; it is
	// kept after a disconnect, so only the same account can be connected again
	Sub string
}

// GoogleAccount returns the Google account connected for sending
func (svc *Service) GoogleAccount(userID uint) (Account, error) {
	t, err := svc.repo.FindByUserID(userID)
	if err != nil || t == nil {
		return Account{}, err
	}
	return Account{Connected: t.Refresh != "", Email: t.GoogleEmail, Sub: t.GoogleSub}, nil
}

// Disconnect revokes the grant at Google (best effort) and forgets it
func (svc *Service) Disconnect(ctx context.Context, userID uint) error {
	t, err := svc.repo.FindByUserID(userID)
	if err != nil || t == nil {
		return err
	}
	if t.Refresh != "" {
		Revoke(ctx, t.Refresh)
	}
	return svc.Invalidate(userID)
}

// Revoke revokes a Google grant by its access or refresh token (best effort)
func Revoke(ctx context.Context, tok string) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://oauth2.googleapis.com/revoke?token="+url.QueryEscape(tok), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
}
