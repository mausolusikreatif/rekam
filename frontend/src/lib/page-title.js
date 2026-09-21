import { writable } from 'svelte/store';

// The browser tab title, in one place.
//
// It was a single static string in index.html, which meant a record shared out
// of /docs arrived in Slack or a search result announcing itself as the bare
// brand line rather than as the thing it is. Titles are addressed
// by whoever knows the content: App.svelte for every route with a fixed name,
// MemoryEditor for a record it has loaded, DocsShell for the docs index.

const BRAND = 'rekam';

// The tagline rides along only on the bare brand — that is, on the landing
// page, the one view whose title has to say what rekam is rather than where in
// it you are. Everywhere else the page name is doing that job already.
const TAGLINE = 'sovereign memory for AI agents';

export const documentTitle = writable(`${BRAND} — ${TAGLINE}`);

// setPageTitle(null) restores the brand line. `docs: true` tags the title as
// coming from the public corpus ("Quick start — rekam docs"), so a link shared
// out of it says where it leads before anyone clicks.
export function setPageTitle(name, { docs = false } = {}) {
  documentTitle.set(name ? `${name} — ${BRAND}${docs ? ' docs' : ''}` : `${BRAND} — ${TAGLINE}`);
}
