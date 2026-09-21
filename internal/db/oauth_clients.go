package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// OAuthClient is a dynamically registered client (RFC 7591) and, crucially,
// the redirect URIs it declared. Registration used to persist nothing —
// /register echoed the URIs back and forgot them — which left /authorize with
// nothing to validate a redirect against, so it delivered authorization codes
// wherever the request asked. See issue #22.
type OAuthClient struct {
	ClientID     string
	RedirectURIs []string
	CreatedAt    time.Time
}

// PutOAuthClient records a registration. Re-registering the same client_id
// replaces its URIs: RFC 7591 clients may re-register, and the alternative
// (first registration wins) would let anyone squat a client_id.
func (r *Registry) PutOAuthClient(clientID string, redirectURIs []string) error {
	if clientID == "" {
		return errors.New("client_id required")
	}
	blob, err := json.Marshal(redirectURIs)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = r.db.Exec(`
		INSERT INTO oauth_clients (client_id, redirect_uris, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(client_id) DO UPDATE SET redirect_uris = excluded.redirect_uris`,
		clientID, string(blob), now)
	return err
}

// OAuthClient returns a registration, or nil when the client is unknown.
func (r *Registry) OAuthClient(clientID string) (*OAuthClient, error) {
	var blob, created string
	err := r.db.QueryRow(
		`SELECT redirect_uris, created_at FROM oauth_clients WHERE client_id = ?`,
		clientID).Scan(&blob, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c := &OAuthClient{ClientID: clientID}
	if err := json.Unmarshal([]byte(blob), &c.RedirectURIs); err != nil {
		return nil, err
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return c, nil
}
