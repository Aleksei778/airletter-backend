package worker

import (
	"errors"
	"net"

	"airletter/internal/token"

	"google.golang.org/api/googleapi"
)

type errorKind int

const (
	// errRetry: temporary problem (rate limit, 5xx, network) — try again later
	errRetry errorKind = iota
	// errPermanent: this email cannot be sent (bad address, rejected message)
	errPermanent
	// errReauth: Google access was revoked — pause all campaigns of the user
	errReauth
)

var retryableReasons = map[string]bool{
	"rateLimitExceeded":     true,
	"userRateLimitExceeded": true,
	"dailyLimitExceeded":    true,
	"quotaExceeded":         true,
	"backendError":          true,
}

func classify(err error) errorKind {
	if token.IsReauthRequired(err) {
		return errReauth
	}

	var gErr *googleapi.Error
	if errors.As(err, &gErr) {
		switch {
		case gErr.Code == 429 || gErr.Code >= 500:
			return errRetry
		case gErr.Code == 401:
			return errReauth
		case gErr.Code == 403:
			for _, e := range gErr.Errors {
				if retryableReasons[e.Reason] {
					return errRetry
				}
				if e.Reason == "insufficientPermissions" {
					return errReauth
				}
			}
			return errPermanent
		default:
			return errPermanent
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return errRetry
	}

	// unknown errors (e.g. context deadline) are retried; asynq caps attempts
	return errRetry
}
