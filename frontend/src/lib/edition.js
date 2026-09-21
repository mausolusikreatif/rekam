// Which edition this bundle was built for, mirroring the server's build tags
// (internal/api/routes_core.go). The server compiles the routes out; this
// keeps the UI from offering surfaces that would 404 against it.
//
// Set at build time by vite.config.js from REKAM_EDITION. Defaults to
// 'managed' for the same reason the Go build does: it is what the deploy
// targets produce, and a forgotten flag should not silently strip features
// from the hosted service.
export const EDITION = __REKAM_EDITION__;

/** Shared corpora: team switcher, members, invites. */
export const hasTeams = EDITION !== 'solo';

/** Self-service signup, the admin console, plan tiers. */
export const hasControlPlane = EDITION === 'managed';
