package sheets

import (
	"context"
	"errors"
	"testing"

	tokenmod "airletter/internal/token"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/api/googleapi"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"not picked", &googleapi.Error{Code: 404, Message: "Requested entity was not found."}, ErrFileAccessRequired},
		{"no permission", &googleapi.Error{Code: 403, Message: "The caller does not have permission"}, ErrFileAccessRequired},
		{"scope unchecked", &googleapi.Error{Code: 403, Message: "Request had insufficient authentication scopes."}, tokenmod.ErrNoToken},
		{"bad range", &googleapi.Error{Code: 400, Message: "Unable to parse range: Sheet9!A2:A"}, ErrInvalidRange},
	}
	for _, c := range cases {
		if got := classify(c.err); !errors.Is(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}

	if got := classify(&googleapi.Error{Code: 500}); errors.Is(got, ErrFileAccessRequired) || errors.Is(got, ErrInvalidRange) {
		t.Errorf("server error classified as user error: %v", got)
	}
}

func TestPickerCodeIsSingleUse(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	if _, err := NewService(nil, rdb, PickerConfig{}).NewPickerCode(ctx, 1); !errors.Is(err, ErrPickerNotConfigured) {
		t.Fatalf("without app ID: %v, want ErrPickerNotConfigured", err)
	}

	svc := NewService(nil, rdb, PickerConfig{APIKey: "key", AppID: "123"})
	code, err := svc.NewPickerCode(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	// take the code out the way PickerSession does, without a Google token
	if id, err := rdb.GetDel(ctx, pickerCodeKey(code)).Uint64(); err != nil || id != 1 {
		t.Fatalf("stored user = %d %v, want 1", id, err)
	}
	if _, err := svc.PickerSession(ctx, code); !errors.Is(err, ErrInvalidPickerCode) {
		t.Errorf("reused code: %v, want ErrInvalidPickerCode", err)
	}
}
