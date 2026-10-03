package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"quicksend/internal/token"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
)

func TestClassify(t *testing.T) {
	gerr := func(code int, reason string) error {
		e := &googleapi.Error{Code: code}
		if reason != "" {
			e.Errors = []googleapi.ErrorItem{{Reason: reason}}
		}
		return fmt.Errorf("send: %w", e)
	}

	tests := []struct {
		name string
		err  error
		want errorKind
	}{
		{"rate limit 429", gerr(429, ""), errRetry},
		{"server error", gerr(503, ""), errRetry},
		{"user rate limit 403", gerr(403, "userRateLimitExceeded"), errRetry},
		{"insufficient permissions", gerr(403, "insufficientPermissions"), errReauth},
		{"forbidden other", gerr(403, "forbidden"), errPermanent},
		{"bad request", gerr(400, "invalidArgument"), errPermanent},
		{"unauthorized", gerr(401, ""), errReauth},
		{"invalid grant", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}, errReauth},
		{"no token", fmt.Errorf("wrap: %w", token.ErrNoToken), errReauth},
		{"deadline", context.DeadlineExceeded, errRetry},
		{"unknown", errors.New("boom"), errRetry},
	}

	for _, tt := range tests {
		if got := classify(tt.err); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
