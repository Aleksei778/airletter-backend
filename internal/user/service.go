package user

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// ErrEmailTaken means the email is already registered
var ErrEmailTaken = errors.New("user: email already registered")

type Service struct {
	db   *gorm.DB
	repo *Repository
}

func NewService(db *gorm.DB, r *Repository) *Service {
	return &Service{db: db, repo: r}
}

func (svc *Service) Create(dto Create) (*User, error) {
	user := &User{
		Email:        dto.Email,
		PasswordHash: dto.PasswordHash,
		FirstName:    strings.TrimSpace(dto.FirstName),
	}

	if err := svc.db.Create(user).Error; err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return nil, ErrEmailTaken
		}
		return nil, err
	}

	return user, nil
}

func (svc *Service) FindByID(id uint) (*User, error) {
	return svc.repo.FindByID(id)
}

func (svc *Service) FindByEmail(email string) (*User, error) {
	return svc.repo.FindByEmail(email)
}

// FillProfile sets name and picture from the connected Google account
// where the user has not set them yet
func (svc *Service) FillProfile(id uint, firstName, lastName, picture string) error {
	updates := map[string]any{}
	u, err := svc.repo.FindByID(id)
	if err != nil || u == nil {
		return err
	}
	if u.FirstName == "" && firstName != "" {
		updates["first_name"] = firstName
		updates["last_name"] = lastName
	}
	if u.PictureUrl == "" && picture != "" {
		updates["picture_url"] = picture
	}
	if len(updates) == 0 {
		return nil
	}
	return svc.db.Model(&User{}).Where("id = ?", id).Updates(updates).Error
}
