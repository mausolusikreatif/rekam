// Package email sends the transactional email rekam's auth/teams flows need
// (signup confirmation, password reset, team invites). Every one of those
// flows predates this package: each mints a token and, for lack of anywhere
// else to put it, returns it directly in the API response for "manual/UI
// delivery" — see internal/api/session.go and teams_handlers.go. That's fine
// for a browser-driven signup where the caller *is* the recipient, but it
// means anyone who knows an email address can call POST /ui/forgot and read
// the reset token straight out of the response, no inbox access required.
// Wiring a real Emailer in closes that gap: see Server.emailer and how each
// handler switches its response shape on whether the send actually
// succeeded, not just on whether a provider is configured.
package email

import "context"

// Message is a plain-text transactional email. No HTML templating yet —
// every message this package sends today is a short "here's your link" note,
// where plain text is fine and one less thing (a template engine, an HTML/text
// multipart body) to keep in sync.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Emailer sends transactional email. Configured reports whether a real
// provider is wired up (an API key present, not just a struct that compiles)
// — callers use it, not a type switch, to decide whether it's safe to omit a
// token from an API response instead of falling back to returning it
// directly. Noop's Configured is always false, so every existing test (which
// never wires an Emailer) keeps seeing exactly the response shape it always
// has.
type Emailer interface {
	Send(ctx context.Context, msg Message) error
	Configured() bool
}
