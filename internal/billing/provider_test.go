package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func sign(secret string, ts int64, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", ts)
	mac.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func TestStripeVerifyWebhook(t *testing.T) {
	s := NewStripe("sk", "whsec_test", "")
	now := time.Unix(1_800_000_000, 0)
	payload := []byte(`{"type":"checkout.session.completed","data":{"object":{"id":"cs_123"}}}`)

	id, err := s.VerifyWebhook(payload, sign("whsec_test", now.Unix(), payload), now)
	if err != nil || id != "cs_123" {
		t.Fatalf("valid: %q %v", id, err)
	}

	bad := []struct {
		name, header string
		payload      []byte
	}{
		{"tampered body", sign("whsec_test", now.Unix(), payload), []byte(`{"type":"checkout.session.completed","data":{"object":{"id":"cs_999"}}}`)},
		{"wrong secret", sign("whsec_other", now.Unix(), payload), payload},
		{"replayed", sign("whsec_test", now.Add(-10*time.Minute).Unix(), payload), payload},
		{"no header", "", payload},
	}
	for _, tt := range bad {
		if _, err := s.VerifyWebhook(tt.payload, tt.header, now); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: got %v, want ErrBadSignature", tt.name, err)
		}
	}

	other := []byte(`{"type":"invoice.paid","data":{"object":{"id":"in_1"}}}`)
	if id, err := s.VerifyWebhook(other, sign("whsec_test", now.Unix(), other), now); err != nil || id != "" {
		t.Errorf("other event: %q %v", id, err)
	}
}

func TestYooKassaCheckoutAndStatus(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != "shop" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/payments":
			if r.Header.Get("Idempotence-Key") != "airletter-7" {
				t.Errorf("idempotence key = %q", r.Header.Get("Idempotence-Key"))
			}
			_ = json.NewDecoder(r.Body).Decode(&got)
			fmt.Fprint(w, `{"id":"yk-1","status":"pending","confirmation":{"confirmation_url":"https://yoomoney.ru/checkout/yk-1"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/payments/yk-1":
			fmt.Fprint(w, `{"id":"yk-1","status":"succeeded"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	y := NewYooKassa("shop", "secret", srv.URL)
	co, err := y.CreateCheckout(context.Background(), CheckoutRequest{
		PaymentID: 7, Amount: 9504_00, Currency: RUB, Description: "d", ReturnURL: "https://site/ru/dashboard/payment?id=7", Locale: "ru",
	})
	if err != nil || co.ExternalID != "yk-1" || co.ConfirmationURL == "" {
		t.Fatalf("checkout: %+v %v", co, err)
	}
	amount := got["amount"].(map[string]any)
	if amount["value"] != "9504.00" || amount["currency"] != "RUB" || got["capture"] != true {
		t.Errorf("request body: %v", got)
	}

	st, err := y.Status(context.Background(), "yk-1")
	if err != nil || st != StatusSucceeded {
		t.Errorf("status: %v %v", st, err)
	}
}

func TestStripeCheckoutAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/checkout/sessions":
			_ = r.ParseForm()
			if r.Form.Get("line_items[0][price_data][unit_amount]") != "11520" ||
				r.Form.Get("line_items[0][price_data][currency]") != "usd" ||
				r.Form.Get("metadata[payment_id]") != "9" || r.Form.Get("mode") != "payment" {
				t.Errorf("form: %v", r.Form)
			}
			fmt.Fprint(w, `{"id":"cs_1","url":"https://checkout.stripe.com/c/cs_1","status":"open","payment_status":"unpaid"}`)
		case r.URL.Path == "/checkout/sessions/cs_1":
			fmt.Fprint(w, `{"id":"cs_1","status":"expired","payment_status":"unpaid"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	s := NewStripe("sk_test", "", srv.URL)
	co, err := s.CreateCheckout(context.Background(), CheckoutRequest{PaymentID: 9, Amount: 115_20, Currency: USD, Description: "d", ReturnURL: "https://site", Locale: "en"})
	if err != nil || co.ExternalID != "cs_1" {
		t.Fatalf("checkout: %+v %v", co, err)
	}
	st, err := s.Status(context.Background(), "cs_1")
	if err != nil || st != StatusCanceled {
		t.Errorf("status: %v %v", st, err)
	}
}
