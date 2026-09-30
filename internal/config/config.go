package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	// DB
	DBHost string `env:"DB_HOST,required"`
	DBPort int    `env:"DB_PORT" envDefault:"5432"`
	DBName string `env:"DB_NAME,required"`
	DBUser string `env:"DB_USER,required"`
	DBPass string `env:"DB_PASS,required"`
	// Apply SQL migrations when the API starts; turn off to run them separately (make migrate-up)
	MigrateOnStart bool `env:"MIGRATE_ON_START" envDefault:"true"`

	// Redis
	RedisURL string `env:"REDIS_URL,required"`

	// Queue (asynq)
	WorkerConcurrency int `env:"WORKER_CONCURRENCY" envDefault:"10"`
	SendMaxRetries    int `env:"SEND_MAX_RETRIES" envDefault:"5"`
	// Pause between two emails of the same user, plus random jitter up to SendJitterSeconds
	SendIntervalSeconds int `env:"SEND_INTERVAL_SECONDS" envDefault:"3"`
	SendJitterSeconds   int `env:"SEND_JITTER_SECONDS" envDefault:"2"`
	// How often the dispatcher releases pending recipients into the queue
	DispatchIntervalSeconds int `env:"DISPATCH_INTERVAL_SECONDS" envDefault:"30"`
	// Max recipients in a single campaign
	MaxRecipientsPerCampaign int `env:"MAX_RECIPIENTS_PER_CAMPAIGN" envDefault:"5000"`
	// Max total size of attachments (raw bytes); Gmail limit is 25MB after base64 encoding
	MaxAttachmentsBytes int64 `env:"MAX_ATTACHMENTS_BYTES" envDefault:"18874368"`

	// JWT
	JWTAccessSecret   string `env:"JWT_ACCESS_SECRET_FOR_AUTH,required"`
	JWTRefreshSecret  string `env:"JWT_REFRESH_SECRET_FOR_AUTH,required"`
	JWTAlgorithm      string `env:"JWT_ALGORITHM" envDefault:"HS256"`
	JWTAccessExpHours int    `env:"JWT_ACCESS_TOKEN_EXPIRATION_HOURS" envDefault:"1"`
	JWTRefreshExpDays int    `env:"JWT_REFRESH_TOKEN_EXPIRATION_DAYS" envDefault:"30"`

	// Google
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID,required"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET,required"`
	WebsiteScopes      []string
	ExtensionScopes    []string

	// App
	FrontendURL string `env:"FRONTEND_URL,required"`
	BackendURL  string `env:"BACKEND_URL,required"`
	ExtensionID string `env:"EXTENSION_ID"`

	// Encryption
	EncryptionKey string `env:"ENCRYPTION_KEY,required"`

	// Session
	SessionSecret string `env:"SESSION_SECRET_KEY,required"`

	// Yookassa
	YookassaShopID    string `env:"YOOKASSA_SHOP_ID"`
	YookassaSecretKey string `env:"YOOKASSA_SECRET_KEY"`
	PaymentReturnURL  string `env:"PAYMENT_RETURN_URL"`

	BuggregatorDSN     string `env:"BUGGREGATOR_DSN"`
	BuggregatorTCPAddr string `env:"BUGGREGATOR_TCP_ADDR" envDefault:"buggregator:9912"`

	// App server
	Port string `env:"APP_PORT" envDefault:"8080"`
	// Secure cookies require HTTPS; disable only for plain-http local development
	CookieSecure bool `env:"COOKIE_SECURE" envDefault:"true"`
}

func (c *Config) ExtensionOrigin() string {
	return "chrome-extension://" + c.ExtensionID
}

func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable TimeZone=UTC",
		c.DBHost, c.DBPort, c.DBUser, c.DBPass, c.DBName,
	)
}

func (c *Config) GoogleRedirectURI() string {
	return c.BackendURL + "/api/auth/google/callback"
}

func Load() (*Config, error) {
	cfg := &Config{
		WebsiteScopes: []string{
			"openid",
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		ExtensionScopes: []string{
			"openid",
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
			"https://www.googleapis.com/auth/gmail.send",
			"https://www.googleapis.com/auth/spreadsheets.readonly",
		},
	}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
