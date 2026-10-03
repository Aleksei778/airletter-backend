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

	// Redis
	RedisURL string `env:"REDIS_URL,required"`

	// Kafka
	KafkaTopic            string `env:"KAFKA_TOPIC,required"`
	KafkaBootstrapServers string `env:"KAFKA_BOOTSTRAP_SERVERS,required"`
	KafkaConsumerGroupID  string `env:"KAFKA_CONSUMER_GROUP_ID" envDefault:"email-consumers"`
	KafkaMaxRetries       int    `env:"KAFKA_CONSUMER_MAX_RETRIES" envDefault:"5"`
	KafkaBaseBackoff      int    `env:"KAFKA_CONSUMER_BASE_BACKOFF" envDefault:"1"`

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
}

func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable TimeZone=UTC",
		c.DBHost, c.DBPort, c.DBUser, c.DBPass, c.DBName,
	)
}

func (c *Config) GoogleRedirectURI() string {
	return c.FrontendURL + "/api/auth/google/callback"
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
