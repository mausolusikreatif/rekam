<script>
  import { onMount } from 'svelte';
  import { activeKey, identities, docsMode, SESSION_KEY } from './lib/store.js';
  import { isDocsPath } from './lib/docs-route.js';
  import { documentTitle, setPageTitle } from './lib/page-title.js';
  import { apiSession } from './lib/api.js';
  import Gate from './components/Gate.svelte';
  import AppShell from './components/AppShell.svelte';
  import MemoryEditor from './components/MemoryEditor.svelte';
  import CommandPalette from './components/CommandPalette.svelte';
  import AgentPreview from './components/AgentPreview.svelte';
  import SkillsView from './components/SkillsView.svelte';
  import LinksView from './components/LinksView.svelte';
  import DeletedView from './components/DeletedView.svelte';
  import TaxonomyGuide from './components/TaxonomyGuide.svelte';
  import Mindmap from './components/Mindmap.svelte';
  import Review from './components/Review.svelte';
  import AdminConsole from './components/AdminConsole.svelte';
  import BillingReturn from './components/BillingReturn.svelte';
  import { hasControlPlane } from './lib/edition.js';
  import DocsShell from './components/DocsShell.svelte';

  let route = { name: 'list' };
  let booting = true;

  // The public docs corpus is decided by the path, not the hash, and decided
  // once: /docs is a different corpus read through a different API, so nothing
  // should be able to flip an authenticated view into it (or out of it) part
  // way through a session. Set before anything loads — api.js reads it on every
  // request, and store.js's canWrite is forced false while it holds.
  // /docs only exists in the managed edition — see lib/edition.js.
  const onDocs = hasControlPlane && isDocsPath();
  docsMode.set(onDocs);

  // Restore a server session (httpOnly cookie) so a reload stays logged in
  // without keeping a raw key in the browser.
  async function restoreSession() {
    try {
      const s = await apiSession();
      if (s.authenticated && s.identity) {
        identities.set([{ key: SESSION_KEY, ...s.identity }]);
        activeKey.set(SESSION_KEY);
      }
    } catch (_) { /* fall through to the gate */ }
    booting = false;
  }

  function parseHash() {
    const hash = window.location.hash.slice(1) || '/';
    const memMatch = hash.match(/^\/memory\/(.+)$/);
    const mapMatch = hash.match(/^\/mindmap\/(.+)$/);
    const newMatch = hash.match(/^\/new\/(.+)$/);
    if (hash === '/login')    route = { name: 'login' };
    else if (hash === '/signup')  route = { name: 'signup' };
    else if (hash === '/new') route = { name: 'new', tax: '' };
    else if (newMatch)        route = { name: 'new', tax: decodeURIComponent(newMatch[1]) };
    else if (hash === '/admin') route = { name: 'admin' };
    else if (hash === '/billing/return') route = { name: 'billing-return' };
    else if (hash === '/agent') route = { name: 'agent' };
    else if (hash === '/skills') route = { name: 'skills' };
    else if (hash === '/links') route = { name: 'links' };
    else if (hash === '/deleted') route = { name: 'deleted' };
    else if (hash === '/taxonomy') route = { name: 'taxonomy' };
    else if (hash === '/review') route = { name: 'review' };
    else if (hash === '/mindmap') route = { name: 'mindmap', id: null };
    else if (mapMatch)         route = { name: 'mindmap', id: mapMatch[1] };
    else if (memMatch)         route = { name: 'editor', id: memMatch[1] };
    else                       route = { name: 'list' };
  }

  // What each route calls itself in the browser tab. Records are the exception
  // — only the component that loaded one knows its title, so MemoryEditor sets
  // that itself and the name here is just what shows while it loads.
  function titleForRoute(r, signedIn) {
    switch (r.name) {
      case 'terms':    return 'Terms of Service';
      case 'privacy':  return 'Privacy Policy';
      // Gate re-titles itself as its state changes; these are what show first.
      // Once signed in, the hash may still read /login or /signup for a beat
      // before hashchange fires, so fall through to the catalog title rather
      // than sticking on the auth-form title.
      case 'login':    return signedIn ? 'Catalog' : 'Log in';
      case 'signup':   return signedIn ? 'Catalog' : 'Create an account';
      case 'admin':    return 'Admin console';
      case 'billing-return': return 'Checkout complete';
      case 'new':      return 'New record';
      case 'agent':    return 'Agent context';
      case 'skills':   return 'Skills';
      case 'links':    return 'Link health';
      case 'deleted':  return 'Deleted records';
      case 'taxonomy': return 'Taxonomy';
      case 'review':   return 'Review';
      case 'mindmap':  return 'Mindmap';
      case 'editor':   return 'Record';
      // The catalog only exists once you are in; logged out, "/" is the
      // landing page, whose title is the brand line.
      default:         return signedIn ? 'Catalog' : null;
    }
  }

  // /docs owns its own titles (DocsShell for the index, MemoryEditor for a
  // record), so leave them alone there.
  $: if (!onDocs) setPageTitle(titleForRoute(route, Boolean($activeKey)));

  // make the landing page a routable public view too:
  //  - "/" or empty hash => landing
  //  - "#/login"          => login/signup form
  onMount(() => {
    // /docs renders the same for everyone and never consults the session, so
    // there is nothing to restore and no hash route to parse — DocsView owns
    // its own path-based routing.
    if (onDocs) {
      booting = false;
      return;
    }
    parseHash();
    restoreSession();
    window.addEventListener('hashchange', parseHash);
    return () => window.removeEventListener('hashchange', parseHash);
  });
</script>

<svelte:head><title>{$documentTitle}</title></svelte:head>

{#if onDocs}
  <DocsShell />
{:else if booting && !$activeKey}
  <!-- brief blank while /ui/session resolves; avoids flashing the gate -->
{:else if !$activeKey}
  <!-- The marketing page and the legal documents are a separate static site
       served at the origin root (frontend/vite.marketing.config.js); this
       bundle is the application, and an unauthenticated visitor to it wants
       the door, not the pitch. #/signup still opens the signup form, which
       is where the landing page's calls to action point. -->
  {#key route.name}<Gate initial={route.name === 'signup' ? 'signup' : 'login'} />{/key}
{:else if route.name === 'admin' && hasControlPlane}
  <AdminConsole />
{:else if route.name === 'billing-return' && hasControlPlane}
  <BillingReturn />
{:else if route.name === 'new'}
  {#key route.tax || 'new'}<MemoryEditor id={null} initialTaxonomy={route.tax} />{/key}
{:else if route.name === 'agent'}
  <AgentPreview />
{:else if route.name === 'skills'}
  <SkillsView />
{:else if route.name === 'links'}
  <LinksView />
{:else if route.name === 'deleted'}
  <DeletedView />
{:else if route.name === 'taxonomy'}
  <TaxonomyGuide />
{:else if route.name === 'review'}
  <Review />
{:else if route.name === 'mindmap'}
  {#key route.id}<Mindmap center={route.id} />{/key}
{:else if route.name === 'editor'}
  {#key route.id}<MemoryEditor id={route.id} />{/key}
{:else}
  <AppShell />
{/if}

{#if $activeKey}
  <CommandPalette />
{/if}
