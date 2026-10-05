// Package whatsapp implements the Meta WhatsApp Cloud API: outbound messages,
// the inbound webhook and its signature check.
package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"
)

// Messenger sends messages to a phone number. The rest of the code depends
// only on this interface, so another provider can be swapped in later.
type Messenger interface {
	SendText(ctx context.Context, to, body string) error
	SendTemplate(ctx context.Context, to, name, lang string, params []string) error
}

// MaxMessageLen is where long replies are split. WhatsApp's hard limit is 4096.
const MaxMessageLen = 3500

// CodeReengagement is the Graph API error returned when free-form text is sent
// outside the 24 hour customer service window.
const CodeReengagement = 131047

// APIError is an error response from the Graph API.
type APIError struct {
	HTTPStatus int
	Code       int    `json:"code"`
	Subcode    int    `json:"error_subcode"`
	Type       string `json:"type"`
	Message    string `json:"message"`
	TraceID    string `json:"fbtrace_id"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("graph api: http %d, code %d: %s", e.HTTPStatus, e.Code, e.Message)
}

// OutsideWindow reports whether the message was rejected because the
// 24 hour window is closed and a template is required.
func (e *APIError) OutsideWindow() bool { return e.Code == CodeReengagement }

func (e *APIError) retryable() bool {
	return e.HTTPStatus == http.StatusTooManyRequests || e.HTTPStatus >= 500
}

// CloudClient sends messages through the WhatsApp Cloud API.
type CloudClient struct {
	BaseURL       string // e.g. https://graph.facebook.com
	GraphVersion  string
	PhoneNumberID string
	Token         string
	HTTP          *http.Client
	Log           *slog.Logger
	MaxAttempts   int
	BaseBackoff   time.Duration
}

// NewCloudClient returns a client with sensible timeouts and retry settings.
func NewCloudClient(baseURL, version, phoneNumberID, token string, log *slog.Logger) *CloudClient {
	return &CloudClient{
		BaseURL:       baseURL,
		GraphVersion:  version,
		PhoneNumberID: phoneNumberID,
		Token:         token,
		HTTP:          &http.Client{Timeout: 15 * time.Second},
		Log:           log,
		MaxAttempts:   4,
		BaseBackoff:   500 * time.Millisecond,
	}
}

// SendText sends a text message, split into several if it is long.
func (c *CloudClient) SendText(ctx context.Context, to, body string) error {
	for _, part := range Split(body, MaxMessageLen) {
		payload := map[string]any{
			"messaging_product": "whatsapp",
			"recipient_type":    "individual",
			"to":                to,
			"type":              "text",
			"text":              map[string]any{"preview_url": false, "body": part},
		}
		if err := c.send(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}

// SendTemplate sends an approved template message with positional body variables.
func (c *CloudClient) SendTemplate(ctx context.Context, to, name, lang string, params []string) error {
	parameters := make([]map[string]string, len(params))
	for i, p := range params {
		parameters[i] = map[string]string{"type": "text", "text": p}
	}
	template := map[string]any{"name": name, "language": map[string]string{"code": lang}}
	if len(parameters) > 0 {
		template["components"] = []map[string]any{{"type": "body", "parameters": parameters}}
	}
	return c.send(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "template",
		"template":          template,
	})
}

func (c *CloudClient) send(ctx context.Context, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/%s/%s/messages", c.BaseURL, c.GraphVersion, c.PhoneNumberID)

	var lastErr error
	for attempt := 1; attempt <= c.MaxAttempts; attempt++ {
		lastErr = c.post(ctx, url, body)
		if lastErr == nil {
			return nil
		}
		var apiErr *APIError
		if errors.As(lastErr, &apiErr) {
			if apiErr.OutsideWindow() {
				c.Log.Error("whatsapp: message rejected because the 24 hour window is closed; message the bot first or use an approved template",
					"code", apiErr.Code, "trace_id", apiErr.TraceID)
				return lastErr
			}
			if !apiErr.retryable() {
				return lastErr
			}
		} else if ctx.Err() != nil {
			return lastErr
		}
		if attempt == c.MaxAttempts {
			break
		}
		wait := c.BaseBackoff << (attempt - 1)
		wait += time.Duration(rand.Int64N(int64(wait)/2 + 1))
		c.Log.Warn("whatsapp: transient send failure, retrying", "attempt", attempt, "wait", wait, "err", lastErr)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastErr
}

func (c *CloudClient) post(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("graph api request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 == 2 {
		return nil
	}

	var wrapper struct {
		Error APIError `json:"error"`
	}
	_ = json.Unmarshal(respBody, &wrapper)
	apiErr := wrapper.Error
	apiErr.HTTPStatus = resp.StatusCode
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(resp.StatusCode)
	}
	return &apiErr
}

// DryRunMessenger logs outbound messages instead of sending them. Used for
// local development with the webhook simulation script.
type DryRunMessenger struct {
	Log *slog.Logger
}

func (d DryRunMessenger) SendText(_ context.Context, to, body string) error {
	for i, part := range Split(body, MaxMessageLen) {
		d.Log.Info("dry-run: would send text", "to", to, "part", i+1, "chars", len([]rune(part)))
		d.Log.Debug("dry-run: message body", "body", part)
	}
	return nil
}

func (d DryRunMessenger) SendTemplate(_ context.Context, to, name, lang string, params []string) error {
	d.Log.Info("dry-run: would send template", "to", to, "template", name, "lang", lang, "params", len(params))
	return nil
}
