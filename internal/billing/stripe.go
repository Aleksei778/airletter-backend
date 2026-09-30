package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Stripe Checkout Sessions: https://docs.stripe.com/api/checkout/sessions
type Stripe struct {
	secretKey, webhookSecret, apiURL string
}

func NewStripe(secretKey, webhookSecret, apiURL string) *Stripe {
	return &Stripe{secretKey: secretKey, webhookSecret: webhookSecret, apiURL: apiURL}
}

func (s *Stripe) Name() string       { return "stripe" }
func (s *Stripe) Currency() Currency { return USD }

type stripeSession struct {
	ID            string `json:"id"`
	URL           string `json:"url"`
	Status        string `json:"status"`         // open, complete, expired
	PaymentStatus string `json:"payment_status"` // paid, unpaid, no_payment_required
}

func (s *Stripe) CreateCheckout(ctx context.Context, req CheckoutRequest) (*Checkout, error) {
	id := strconv.FormatUint(uint64(req.PaymentID), 10)
	form := url.Values{
		"mode":                                   {"payment"},
		"success_url":                            {req.ReturnURL},
		"cancel_url":                             {req.ReturnURL},
		"client_reference_id":                    {id},
		"metadata[payment_id]":                   {id},
		"locale":                                 {req.Locale},
		"line_items[0][quantity]":                {"1"},
		"line_items[0][price_data][currency]":    {strings.ToLower(string(req.Currency))},
		"line_items[0][price_data][unit_amount]": {strconv.FormatInt(req.Amount, 10)},
		"line_items[0][price_data][product_data][name]": {req.Description},
	}
	if req.Email != "" {
		form.Set("customer_email", req.Email)
	}

	var sess stripeSession
	if err := s.do(ctx, http.MethodPost, "/checkout/sessions", form, "airletter-"+id, &sess); err != nil {
		return nil, err
	}
	return &Checkout{ExternalID: sess.ID, ConfirmationURL: sess.URL}, nil
}

func (s *Stripe) Status(ctx context.Context, externalID string) (Status, error) {
	var sess stripeSession
	if err := s.do(ctx, http.MethodGet, "/checkout/sessions/"+externalID, nil, "", &sess); err != nil {
		return "", err
	}
	switch {
	case sess.PaymentStatus == "paid":
		return StatusSucceeded, nil
	case sess.Status == "expired":
		return StatusCanceled, nil
	default:
		return StatusPending, nil
	}
}

func (s *Stripe) do(ctx context.Context, method, path string, form url.Values, idempotencyKey string, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, s.apiURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.secretKey)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("stripe: %s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}

// signatureTolerance limits replay of old webhook deliveries
const signatureTolerance = 5 * time.Minute

// VerifyWebhook checks the Stripe-Signature header and returns the session id
// of a checkout.session.* event ("" for other events).
// https://docs.stripe.com/webhooks#verify-manually
func (s *Stripe) VerifyWebhook(payload []byte, header string, now time.Time) (string, error) {
	if s.webhookSecret == "" {
		return "", ErrBadSignature
	}

	var ts int64
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts, _ = strconv.ParseInt(v, 10, 64)
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == 0 || len(sigs) == 0 {
		return "", ErrBadSignature
	}
	if d := now.Sub(time.Unix(ts, 0)); d > signatureTolerance || d < -signatureTolerance {
		return "", ErrBadSignature
	}

	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "."))
	mac.Write(payload)
	expected := mac.Sum(nil)

	valid := false
	for _, sig := range sigs {
		if got, err := hex.DecodeString(sig); err == nil && hmac.Equal(got, expected) {
			valid = true
		}
	}
	if !valid {
		return "", ErrBadSignature
	}

	var event struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID string `json:"id"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return "", fmt.Errorf("stripe: parse event: %w", err)
	}
	if !strings.HasPrefix(event.Type, "checkout.session.") {
		return "", nil
	}
	return event.Data.Object.ID, nil
}
