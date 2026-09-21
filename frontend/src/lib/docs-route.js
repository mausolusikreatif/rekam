import { writable } from 'svelte/store';

// Routing for the public docs corpus at /docs.
//
// The rest of the app routes on the hash, which is right for a private tool
// nobody links to from outside. Docs are the opposite: people share them, and a
// search engine indexes them, so these are real paths — /docs and
// /docs/<record id>. The server serves the SPA shell for both (see the GET /
// catch-all in internal/api/server.go), and the public API deliberately lives
// under /public so it cannot collide with a record id here.

export const DOCS_BASE = '/docs';

// Records are addressed by a slug of their title, not by id. A docs URL is
// meant to be pasted into a chat and read by a search engine, and
// /docs/what-rekam-is says what /docs/f181b138-089a-4f56-bac8-c5c11116aab0
// cannot. DocsView resolves the slug back to an id against the corpus index;
// an unrecognised slug is retried as a raw id, so an older link still opens.
export function slugify(title) {
  return String(title)
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[^a-z0-9\s-]/g, '')
    .trim()
    .replace(/[\s_-]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

// The slug of the record currently open, or null for the index. Kept in a store
// rather than component state so the browser's back button and an in-page link
// both move the same thing.
export const docsSlug = writable(slugFromPath());

// isDocsPath reports whether the current URL belongs to the docs corpus.
// Matches /docs and /docs/<anything>, but not a path that merely starts with
// those letters (/docsomething is not the docs page).
export function isDocsPath(pathname = window.location.pathname) {
  const p = pathname.replace(/\/+$/, '');
  return p === DOCS_BASE || p.startsWith(`${DOCS_BASE}/`);
}

export function slugFromPath(pathname = window.location.pathname) {
  const p = pathname.replace(/\/+$/, '');
  if (!p.startsWith(`${DOCS_BASE}/`)) return null;
  const slug = p.slice(DOCS_BASE.length + 1);
  return slug ? decodeURIComponent(slug) : null;
}

// openDocsRecord navigates within the docs corpus. Takes the record itself
// ({ id, title }) so it can address it by slug, or null to go back to the
// index. It pushes a real history entry, so Back does what a reader expects and
// the address bar always holds a URL worth copying.
export function openDocsRecord(record) {
  const slug = record ? slugify(record.title) || record.id : null;
  const path = slug ? `${DOCS_BASE}/${encodeURIComponent(slug)}` : DOCS_BASE;
  if (path !== window.location.pathname) {
    window.history.pushState({}, '', path);
  }
  docsSlug.set(slug);
  window.scrollTo(0, 0);
}

// listenForDocsNavigation keeps the store in step with the back/forward
// buttons. Returns an unsubscribe function for onMount to hand back.
export function listenForDocsNavigation() {
  const onPop = () => docsSlug.set(slugFromPath());
  window.addEventListener('popstate', onPop);
  return () => window.removeEventListener('popstate', onPop);
}
