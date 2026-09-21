<script>
  import { onMount } from 'svelte';
  import { apiMemories } from '../lib/api.js';
  import { docsSlug, slugify, listenForDocsNavigation } from '../lib/docs-route.js';
  import { setPageTitle } from '../lib/page-title.js';
  import AppShell from './AppShell.svelte';
  import MemoryEditor from './MemoryEditor.svelte';
  import CommandPalette from './CommandPalette.svelte';

  // The public docs corpus renders in the real application shell — the same
  // sidebar, toolbar, catalog table, command palette, and record view a
  // signed-in user gets. That is deliberate: /docs is the documentation and the
  // demo at once, so anything rebuilt to merely resemble the app would drift
  // from it. Each of those components hides what cannot exist here by checking
  // the docsMode store, and store.js forces canWrite false while it holds.
  //
  // The only thing this adds is routing. The app addresses a record by id in
  // the hash; docs address it by a slug of its title in the path, so a link is
  // worth sharing. Resolving one to the other needs the corpus index, which is
  // one request against a corpus small enough to fetch whole.

  let index = new Map();
  let resolved = false;

  onMount(() => {
    const stopListening = listenForDocsNavigation();
    loadIndex();
    return stopListening;
  });

  async function loadIndex() {
    try {
      const data = await apiMemories({});
      index = new Map((data.memories || []).map((m) => [slugify(m.title) || m.id, m]));
    } catch (_) {
      index = new Map();
    } finally {
      resolved = true;
    }
  }

  // An id still resolves, so a link made before a record was renamed, or one
  // copied out of the API, keeps working.
  $: record = $docsSlug
    ? index.get($docsSlug) || [...index.values()].find((m) => m.id === $docsSlug)
    : null;
  $: missing = $docsSlug && resolved && !record;

  // MemoryEditor names the tab while a record is open; this covers the index
  // and the moment a record closes again.
  $: if (!record) setPageTitle('Docs');
</script>

{#if record}
  {#key record.id}
    <MemoryEditor id={record.id} />
  {/key}
{:else if missing}
  <div class="docs-missing">
    <p class="docs-missing__title">No record at that address</p>
    <p class="docs-missing__sub">The record may have been renamed since this link was made.</p>
    <a class="btn-ghost" href="/docs">Go to the catalog</a>
  </div>
{:else if $docsSlug}
  <div class="docs-missing"><div class="spinner"></div></div>
{:else}
  <AppShell />
{/if}

<CommandPalette />

<style>
  .docs-missing {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    background: var(--bg);
    padding: 24px;
    text-align: center;
  }
  .docs-missing__title {
    margin: 0;
    font-family: var(--font-display);
    font-size: 20px;
    font-weight: 540;
    color: var(--text);
  }
  .docs-missing__sub { margin: 0 0 8px; font-size: 14px; color: var(--text-2); }
  .docs-missing :global(.btn-ghost) { text-decoration: none; }
</style>
