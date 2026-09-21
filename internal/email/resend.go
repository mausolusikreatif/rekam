package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// resendDefaultEndpoint is Resend's send-email API. See
// https://resend.com/docs/api-reference/emails/send-email.
const resendDefaultEndpoint = "https://api.resend.com/emails"

// Resend sends email via Resend's HTTP API.
type Resend struct {
	APIKey string
	From   string // verified sender, e.g. "rekam <noreply@rekam.dev>"

	// Endpoint and Client are overridable for tests; both default when zero
	// (Endpoint to resendDefaultEndpoint, Client to http.DefaultClient), so
	// production construction only ever needs APIKey and From.
	Endpoint string
	Client   *http.Client
}

// Configured reports whether both an API key and a verified sender are set —
// the two things every send actually needs. A zero-value Resend (e.g. one
// nobody bothered to fill in) is correctly "not configured", not a broken one
// that errors on every send.
func (r Resend) Configured() bool {
	return r.APIKey != "" && r.From != ""
}

func (r Resend) Send(ctx context.Context, msg Message) error {
	if !r.Configured() {
		return fmt.Errorf("resend: not configured (missing api key or from address)")
	}
	body, err := json.Marshal(map[string]any{
		"from":    r.From,
		"to":      []string{msg.To},
		"subject": msg.Subject,
		"text":    msg.Text,
	})
	if err != nil {
		return fmt.Errorf("resend: encode request: %w", err)
	}

	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = resendDefaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("resend: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("resend: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("resend: %s: %s", resp.Status, detail)
	}
	return nil
}
