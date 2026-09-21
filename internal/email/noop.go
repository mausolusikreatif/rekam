package email

import "context"

// Noop is the default Emailer when no provider is configured. Send is a
// silent no-op; Configured always reports false, which is how a handler
// knows to fall back to its old behavior — return the token in the API
// response — rather than claim an email went out that never did.
type Noop struct{}

func (Noop) Send(context.Context, Message) error { return nil }

func (Noop) Configured() bool { return false }
