package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// YooKassa API v3: https://yookassa.ru/developers/api
type YooKassa struct {
	shopID, secretKey, apiURL string
}

func NewYooKassa(shopID, secretKey, apiURL string) *YooKassa {
	return &YooKassa{shopID: shopID, secretKey: secretKey, apiURL: apiURL}
}

func (y *YooKassa) Name() string       { return "yookassa" }
func (y *YooKassa) Currency() Currency { return RUB }

type ykAmount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

type ykPayment struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Confirmation struct {
		URL string `json:"confirmation_url"`
	} `json:"confirmation"`
}

func (y *YooKassa) CreateCheckout(ctx context.Context, req CheckoutRequest) (*Checkout, error) {
	locale := "ru_RU"
	if req.Locale == "en" {
		locale = "en_US"
	}
	body := map[string]any{
		"amount":  ykAmount{Value: FormatAmount(req.Amount), Currency: string(req.Currency)},
		"capture": true,
		"confirmation": map[string]string{
			"type": "redirect", "return_url": req.ReturnURL, "locale": locale,
		},
		// for the self-employed the description becomes the service name in the receipt
		"description": req.Description,
		"metadata":    map[string]string{"payment_id": strconv.FormatUint(uint64(req.PaymentID), 10)},
	}

	var p ykPayment
	// the idempotence key makes a retried request return the same payment
	if err := y.do(ctx, http.MethodPost, "/payments", body, fmt.Sprintf("airletter-%d", req.PaymentID), &p); err != nil {
		return nil, err
	}
	if p.Confirmation.URL == "" {
		return nil, fmt.Errorf("yookassa: no confirmation url for payment %s", p.ID)
	}
	return &Checkout{ExternalID: p.ID, ConfirmationURL: p.Confirmation.URL}, nil
}

func (y *YooKassa) Status(ctx context.Context, externalID string) (Status, error) {
	var p ykPayment
	if err := y.do(ctx, http.MethodGet, "/payments/"+externalID, nil, "", &p); err != nil {
		return "", err
	}
	switch p.Status {
	case "succeeded":
		return StatusSucceeded, nil
	case "canceled":
		return StatusCanceled, nil
	default: // pending, waiting_for_capture
		return StatusPending, nil
	}
}

func (y *YooKassa) do(ctx context.Context, method, path string, body any, idempotenceKey string, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, y.apiURL+path, reader)
	if err != nil {
		return err
	}
	req.SetBasicAuth(y.shopID, y.secretKey)
	req.Header.Set("Content-Type", "application/json")
	if idempotenceKey != "" {
		req.Header.Set("Idempotence-Key", idempotenceKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("yookassa: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("yookassa: %s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}
