package logger

import (
	"airletter/internal/config"

	"github.com/getsentry/sentry-go"
)

func Init(cfg *config.Config) error {
	return sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.BuggregatorDSN,
		AttachStacktrace: true,
	})
}

func Error(err error) {
	sentry.CaptureException(err)
}

func Message(msg string) {
	sentry.CaptureMessage(msg)
}
