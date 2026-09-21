// Where the landing page's assets and links point, decided at build time.
//
// The page ships in two places: the marketing site at the site root, and
// (historically) the app bundle. Those disagree about two things — where the
// media lives, and whether a "sign up" link stays on this page or crosses
// into the app — so both are build-time constants rather than assumptions
// baked into the markup.
export const MEDIA_BASE = __REKAM_MEDIA_BASE__;

// Prefix for links that land in the app. Empty inside the app itself, where
// a bare hash route is already in the right document.
export const APP_BASE = __REKAM_APP_BASE__;

/** Link into an app hash route from wherever this page is served. */
export const appLink = (hash) => `${APP_BASE}#${hash}`;

// Where the docs link points. On the marketing site this is an absolute
// https://app.rekam.net/docs — content-heavy and read-only, so it goes
// straight at the managed origin instead of through the Worker's proxy.
// Inside the app itself it stays "/docs", same document, no crossing.
export const DOCS_URL = __REKAM_DOCS_URL__;
