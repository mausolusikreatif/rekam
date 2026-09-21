import { writable, derived } from 'svelte/store';

// SESSION_KEY is a sentinel activeKey value meaning "authenticate via the
// httpOnly session cookie" rather than a raw Bearer key. The real credential
// never touches JS or localStorage in this mode.
export const SESSION_KEY = '__session__';

// Raw keys are no longer persisted to localStorage. Browser auth is the
// server-side session (httpOnly cookie); any manually pasted key lives only in
// memory for the current tab. identities is metadata only in session mode.
export const identities = writable([]);
export const activeKey  = writable(null);

// The team (shared corpus) the UI is currently acting in. null or {home:true}
// means the personal home corpus — no team selector is sent. A team id is not a
// secret, so the selection is persisted to survive reloads (unlike raw keys).
const TEAM_LS = 'rekam.team';
function loadTeam() {
  try { const v = localStorage.getItem(TEAM_LS); return v ? JSON.parse(v) : null; }
  catch { return null; }
}
export const currentTeam = writable(loadTeam());
currentTeam.subscribe((t) => {
  try {
    if (t && t.id && !t.home) localStorage.setItem(TEAM_LS, JSON.stringify(t));
    else localStorage.removeItem(TEAM_LS);
  } catch { /* private mode: selection just won't persist */ }
});
// The caller's teams (personal home + joined), populated by the team switcher.
export const teams = writable([]);
export const memories   = writable([]);
export const memoryTotal  = writable(0);
export const memoryOffset = writable(0);
export const searchQuery  = writable('');
export const searchMode   = writable(false);
export const taxonomyFilter = writable('');
export const catalog = writable([]);
export const loading = writable(false);
export const graphView = writable(false);
export const paletteOpen = writable(false);
// When set to a link target (title), the reading view scrolls to and flashes the
// matching [[wiki-link]] chip on next render — used to jump to a specific broken
// link from the link-health badge. Cleared once consumed.
export const focusLink = writable(null);
// Number of spaced-repetition cards due now, shown as a badge in the sidebar.
export const dueCount = writable(0);

// Paper by day, kraft by lamplight. 'system' follows the OS (the default —
// global.css handles that half in plain CSS via prefers-color-scheme, so a
// first-time visitor gets the right theme with no flash and no JS). Picking
// 'light' or 'dark' explicitly overrides the OS and is remembered.
// Every theme the picker offers. 'swatch'/'accent' are only for the picker's
// own preview dots — the theme itself lives entirely in global.css (see the
// "Theme system" block there); adding one here without a matching
// [data-theme="<id>"] block just falls back to whatever :root resolves to.
// 'malleable' is lifted from the Omarchy theme of the same name on this
// machine (~/.config/omarchy/themes/malleable/colors.toml) — proof the token
// system takes a genuinely different palette, not just a second paper color.
export const THEMES = [
  { id: 'light',      label: 'Paper',      swatch: '#f6f2e9', accent: '#2e4a78' },
  { id: 'dark',       label: 'Kraft',      swatch: '#1c1a15', accent: '#7fa0d9' },
  { id: 'malleable',  label: 'Malleable',  swatch: '#0a0c0f', accent: '#7fd4e0' },
];

const THEME_LS = 'rekam.theme';
function loadTheme() {
  try { return localStorage.getItem(THEME_LS) || 'system'; }
  catch { return 'system'; }
}
export const theme = writable(loadTheme());
theme.subscribe((t) => {
  try {
    if (t !== 'system') {
      document.documentElement.setAttribute('data-theme', t);
      localStorage.setItem(THEME_LS, t);
    } else {
      document.documentElement.removeAttribute('data-theme');
      localStorage.removeItem(THEME_LS);
    }
  } catch { /* SSR-less app, but guard anyway */ }
});

// True while the app is rendering the public docs corpus at /docs. It is set
// once, from the route, before anything loads — see App.svelte. Two things key
// off it: api.js sends every request to the read-only /public mirror instead of
// the authenticated API, and canWrite below is forced false.
export const docsMode = writable(false);

// The identity matching the active key (or null), and whether it may write.
export const currentIdentity = derived([identities, activeKey], ([$ids, $key]) =>
  $ids.find(i => i.key === $key) || null);
// Write permission is the account gate AND the team role: a Viewer in a team is
// read-only regardless of the account flag, so write affordances hide for them.
export const canWrite = derived([currentIdentity, currentTeam, docsMode], ([$id, $team, $docs]) => {
  // The public docs corpus is read-only for everyone, including a signed-in
  // user who happens to open /docs in the same browser. Checked first so no
  // combination of identity and team can talk its way past it — the server
  // enforces this too, but every write affordance should be gone from the UI
  // rather than failing on click.
  if ($docs) return false;
  const accountOk = $id ? $id.allow_write !== false : true;
  if ($team && $team.id && !$team.home) return accountOk && $team.role !== 'viewer';
  return accountOk;
});
