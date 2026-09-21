import { activeKey, currentTeam, docsMode, SESSION_KEY } from './store.js';
import { get } from 'svelte/store';

// ── Public docs corpus ────────────────────────────────────────────────────────
// On /docs the app reads one published corpus through the server's anonymous
// mirror at /public. Routing that here, at the chokepoint every request already
// passes through, is what lets the ordinary components render the docs corpus
// without knowing it is any different from a signed-in one.
//
// The mirror exposes a short list of read endpoints and nothing else — see
// internal/api/public.go, which is the authority. DOCS_READS mirrors that list,
// and a request for anything outside it means a component that has no business
// on /docs got mounted there, so it fails here rather than quietly falling
// through to the authenticated API.
const DOCS_READS = [
  /^\/catalog$/,
  /^\/memories$/,
  /^\/search$/,
  /^\/memory\/[^/]+$/,
  /^\/memory\/[^/]+\/revisions$/,
];

function docsRequest(path) {
  const [pathname, query] = path.split('?');
  if (!DOCS_READS.some((re) => re.test(pathname))) {
    throw { error: `${pathname} is not part of the public docs corpus` };
  }
  // No cookie, no Bearer, no team selector. The server picks the corpus and
  // discards anything the caller sends, so attaching a signed-in user's session
  // would only make a page that must render identically for everyone look like
  // it might not.
  return fetch(`/public${pathname}${query ? `?${query}` : ''}`, { credentials: 'omit' });
}

// authHeaders builds the auth + team-selector headers every request shares. In
// session mode (SESSION_KEY sentinel) the httpOnly cookie carries auth, so no
// Bearer is sent; a real key still uses Bearer. When a team is selected, the
// X-Rekam-Team selector routes the request into that team's corpus.
function authHeaders(key) {
  const h = {};
  if (key && key !== SESSION_KEY) h['Authorization'] = `Bearer ${key}`;
  const team = get(currentTeam);
  if (team && team.id && !team.home) h['X-Rekam-Team'] = team.id;
  return h;
}

async function apiFetch(path, opts = {}) {
  let res;
  if (get(docsMode)) {
    if (opts.method && opts.method !== 'GET') throw { error: 'the public docs corpus is read-only' };
    res = await docsRequest(path);
  } else {
    const key = opts.key || get(activeKey);
    const headers = { 'Content-Type': 'application/json', ...authHeaders(key), ...(opts.headers || {}) };
    res = await fetch(path, { credentials: 'same-origin', ...opts, headers });
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw body;
  }
  return res.json();
}

// Upload an image blob. Returns { id, url } where url is a capability URL
// (/files/<identity>/<id>) usable directly in an <img src>. Uses multipart, so
// it can't go through apiFetch (which forces JSON) — the browser sets the
// multipart boundary itself when Content-Type is left unset. Auth mirrors
// apiFetch: in session mode the httpOnly cookie carries auth (no Bearer); a real
// pasted/legacy key still uses Bearer.
export async function apiUploadFile(file) {
  const key = get(activeKey);
  const form = new FormData();
  form.append('file', file);
  const headers = authHeaders(key);
  const res = await fetch('/files', {
    method: 'POST',
    credentials: 'same-origin',
    headers,
    body: form,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw body;
  }
  return res.json();
}

// ── Browser session (httpOnly cookie) ──────────────────────────────────────────

async function postJSON(path, body) {
  const res = await fetch(path, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({ error: res.statusText }));
  if (!res.ok) throw data;
  return data;
}

// Log in with email + password; the server sets an httpOnly session cookie
// scoped to that user's own identity/memory.
export const apiLogin = (email, password) => postJSON('/ui/login', { email, password });

// Self-service sign-up: creates the account + private memory and returns an
// unconfirmed identity + confirm token. The caller must follow the confirmation
// link before the account can log in.
export const apiSignup = (email, password, name) =>
  postJSON('/ui/signup', { email, password, name });

// Request a password-reset token for the given email. Returns the token for
// manual/email delivery when no SMTP is configured yet.
export const apiForgot = (email) => postJSON('/ui/forgot', { email });

// Confirm an account with a token (from the confirmation link/code).
export const apiConfirm = (token) => fetch(`/ui/confirm?token=${encodeURIComponent(token)}`, {
  method: 'GET',
  credentials: 'same-origin',
  headers: { 'Content-Type': 'application/json' },
}).then(async (res) => {
  const data = await res.json().catch(() => ({ error: res.statusText }));
  if (!res.ok) throw data;
  return data;
});

// Reset password with a valid reset token.
export const apiReset = (token, password) => postJSON(`/ui/reset?token=${encodeURIComponent(token)}`, { password });

// Report the current session state (does not require a key).
export async function apiSession() {
  const res = await fetch('/ui/session', { credentials: 'same-origin' });
  if (!res.ok) return { authenticated: false };
  return res.json();
}

// Clear the server session and cookie.
export async function apiLogout() {
  await fetch('/ui/logout', { method: 'POST', credentials: 'same-origin' }).catch(() => {});
}

// ── Billing (Dodo Payments) ─────────────────────────────────────────────────
// Opens a hosted Dodo checkout session for a plan the caller can buy —
// "managed" (the Pro seat upgrade) or "self_host_team" (the flat, unlimited-
// seat licence to run the team binary). Returns { checkout_url }; the caller
// redirects the browser there. Entitlement is granted from the webhook once
// Dodo confirms payment, never from this call or the return_url redirect —
// see internal/api/payments_dodo.go.
export const apiCreateCheckout = (kind, returnUrl) =>
  apiFetch('/billing/checkout', { method: 'POST', body: JSON.stringify({ kind, return_url: returnUrl }) });

// ── Teams ───────────────────────────────────────────────────────────────────
// The corpora the caller can act in (personal home + joined teams), each with
// the caller's role, for the team switcher.
export const apiTeams = () => apiFetch('/teams');
export const apiCreateTeam = (name) =>
  apiFetch('/teams', { method: 'POST', body: JSON.stringify({ name }) });
// The following act on the currently-selected team (via the X-Rekam-Team header
// that apiFetch injects), so they are context-sensitive by design.
export const apiMyMembership = () => apiFetch('/team/membership');
export const apiTeamMembers = () => apiFetch('/team/members');
export const apiRenameTeam = (name) =>
  apiFetch('/team', { method: 'PATCH', body: JSON.stringify({ name }) });
// Soft delete: the team leaves every switcher and stops being selectable, but
// its records are not destroyed. Owner-only, server-enforced.
export const apiDeleteTeam = () => apiFetch('/team', { method: 'DELETE' });
// Resolve an email to an invitable identity in the current team (manager-only,
// exact match). Returns { identity_id, name, email } or throws not_found.
export const apiLookupMember = (email) =>
  apiFetch(`/team/lookup?email=${encodeURIComponent(email)}`);
export const apiSetMember = (identityId, { role, read_grants = [], title_grants = [] }) =>
  apiFetch(`/team/members/${identityId}`, {
    method: 'PUT',
    body: JSON.stringify({ role, read_grants, title_grants }),
  });
export const apiRemoveMember = (identityId) =>
  apiFetch(`/team/members/${identityId}`, { method: 'DELETE' });

export const apiMe = (key) => apiFetch('/me', { key });
export const apiIdentities = () => apiFetch('/identities');
export const apiMemories = ({ taxonomy = '', limit = 0, offset = 0 } = {}) => {
  const params = new URLSearchParams();
  if (taxonomy) params.set('taxonomy', taxonomy);
  if (limit > 0)  params.set('limit',  String(limit));
  if (offset > 0) params.set('offset', String(offset));
  const qs = params.toString() ? `?${params}` : '';
  return apiFetch(`/memories${qs}`);
};
export const apiSearch = (q, taxonomy = '') => {
  const params = new URLSearchParams({ q });
  if (taxonomy) params.set('taxonomy', taxonomy);
  return apiFetch(`/search?${params}`);
};
export const apiGetMemory = (id) => apiFetch(`/memory/${id}`);
export const apiCreateMemory = (body) => apiFetch('/memory', { method: 'POST', body: JSON.stringify(body) });
export const apiUpdateMemory = (id, body) => apiFetch(`/memory/${id}`, { method: 'PATCH', body: JSON.stringify(body) });
export const apiDeleteMemory = (id) => apiFetch(`/memory/${id}`, { method: 'DELETE' });

// ── Versioning ────────────────────────────────────────────────────────────────
// A memory's revision history (newest first), each a full snapshot of a version.
export const apiMemoryHistory = (id) => apiFetch(`/memory/${id}/revisions`);
// Restore a past version: copies its fields forward as a new version (the counter
// never rewinds). Returns the updated memory.
export const apiRestoreRevision = (id, version) =>
  apiFetch(`/memory/${id}/restore/${version}`, { method: 'POST' });
// Tombstones for deleted records in the current corpus (permanent audit trail).
export const apiDeletedMemories = ({ taxonomy = '', limit = 0 } = {}) => {
  const params = new URLSearchParams();
  if (taxonomy) params.set('taxonomy', taxonomy);
  if (limit > 0) params.set('limit', String(limit));
  const qs = params.toString() ? `?${params}` : '';
  return apiFetch(`/deleted${qs}`);
};
export const apiCatalog = () => apiFetch('/catalog');

// ── Taxonomy template ──────────────────────────────────────────────────────────
// The corpus's classification scaffold: branches with "what goes here" notes,
// used to onboard people and agents. Returns { branches, source, kind, can_edit }.
export const apiTaxonomyTemplate = () => apiFetch('/taxonomy/template');
// Replace the template (owner/admin only). An empty branches list reverts to the
// shipped default. Each branch is { path, description }.
export const apiSetTaxonomyTemplate = (branches) =>
  apiFetch('/taxonomy/template', { method: 'PUT', body: JSON.stringify({ branches }) });
export const apiEdgeHealth = () => apiFetch('/admin/edge-health');

// ── Admin console ───────────────────────────────────────────────────────────────
export const apiAdminStats = () => apiFetch('/admin/stats');
export const apiAdminUsers = () => apiFetch('/admin/users');
export const apiAdminCreateUser = (body) =>
  apiFetch('/admin/users', { method: 'POST', body: JSON.stringify(body) });
export const apiAdminUpdateUser = (id, body) =>
  apiFetch(`/admin/users/${id}`, { method: 'PATCH', body: JSON.stringify(body) });
export const apiAdminDeleteUser = (id) =>
  apiFetch(`/admin/users/${id}`, { method: 'DELETE' });
export const apiAdminGrants = () => apiFetch('/admin/grants');
export const apiAdminRevokeGrant = (id) =>
  apiFetch(`/admin/grants/${id}`, { method: 'DELETE' });
export const apiAdminTeams = () => apiFetch('/admin/teams');
export const apiAdminDeleteTeam = (id) =>
  apiFetch(`/admin/teams/${id}`, { method: 'DELETE' });

// Fetch the link graph. With `center` (a memory id) this is the ego graph out to
// `depth` hops; otherwise the whole corpus, optionally scoped to a taxonomy.
export const apiGraph = ({ center = '', depth = 0, taxonomy = '' } = {}) => {
  const params = new URLSearchParams();
  if (center) params.set('center', center);
  if (depth > 0) params.set('depth', String(depth));
  if (taxonomy) params.set('taxonomy', taxonomy);
  const qs = params.toString() ? `?${params}` : '';
  return apiFetch(`/graph${qs}`);
};

// Suggested links: related-but-unlinked record pairs.
export const apiSuggestLinks = (limit = 0) =>
  apiFetch(`/suggest-links${limit > 0 ? `?limit=${limit}` : ''}`);

// Spaced repetition.
export const apiReviewDue = (limit = 0) =>
  apiFetch(`/review${limit > 0 ? `?limit=${limit}` : ''}`);
export const apiAddReview = (id) => apiFetch(`/review/${id}`, { method: 'POST' });
export const apiRemoveReview = (id) => apiFetch(`/review/${id}`, { method: 'DELETE' });
export const apiGradeReview = (id, grade) =>
  apiFetch(`/review/${id}/grade`, { method: 'POST', body: JSON.stringify({ grade }) });

// Fetch the full catalog as a zip of Markdown files. Returns the blob plus the
// server-suggested filename so the caller can trigger a download.
export async function apiExportZip() {
  const key = get(activeKey);
  const headers = authHeaders(key);
  const res = await fetch('/export', { credentials: 'same-origin', headers });
  if (!res.ok) throw await res.json().catch(() => ({ error: res.statusText }));
  const blob = await res.blob();
  const cd = res.headers.get('Content-Disposition') || '';
  const m = cd.match(/filename="?([^"]+)"?/);
  return { blob, filename: m ? m[1] : 'rekam-export.zip' };
}

// Fetch a single record as a Markdown file (same front-matter format as the zip
// export). Returns the blob plus the server-suggested filename.
export async function apiExportMemory(id) {
  const key = get(activeKey);
  const res = await fetch(`/export/${id}`, { credentials: 'same-origin', headers: authHeaders(key) });
  if (!res.ok) throw await res.json().catch(() => ({ error: res.statusText }));
  const blob = await res.blob();
  const cd = res.headers.get('Content-Disposition') || '';
  const m = cd.match(/filename="?([^"]+)"?/);
  return { blob, filename: m ? m[1] : 'record.md' };
}
