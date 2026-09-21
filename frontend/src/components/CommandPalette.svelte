<script>
  import { tick, onMount } from 'svelte';
  import { paletteOpen, activeKey, canWrite, catalog, graphView,
           taxonomyFilter, searchMode, searchQuery, docsMode } from '../lib/store.js';
  import { apiMemories, apiSearch, apiCatalog, apiExportZip } from '../lib/api.js';
  import { loadMemories } from '../lib/data.js';
  import { debounce, downloadBlob } from '../lib/utils.js';
  import { openDocsRecord } from '../lib/docs-route.js';

  let query = '';
  let activeIndex = 0;
  let inputEl;
  let listEl;
  let recent = [];
  let recordResults = [];

  // ── Open / close lifecycle ────────────────────────────────────────────────
  // Subscribe rather than track a previous value reactively: Svelte orders
  // reactive statements by dependency, which makes the "prevOpen" pattern fire
  // unreliably.
  onMount(() => paletteOpen.subscribe(v => { if (v) onOpen(); }));

  async function onOpen() {
    query = '';
    recordResults = [];
    activeIndex = 0;
    await tick();
    inputEl?.focus();
    if ($catalog.length === 0) apiCatalog().then(d => catalog.set(d.taxonomy || [])).catch(() => {});
    try { recent = (await apiMemories({ limit: 7 })).memories || []; } catch (_) { recent = []; }
  }

  function close() { paletteOpen.set(false); }

  // ── Search (FTS) ──────────────────────────────────────────────────────────
  const runSearch = debounce(async (q) => {
    const t = q.trim();
    if (!t) { recordResults = []; return; }
    try { recordResults = (await apiSearch(t)).results || []; }
    catch (_) { recordResults = []; }
  }, 160);
  $: if ($paletteOpen) runSearch(query);

  // ── Match highlighting + snippets ─────────────────────────────────────────
  $: terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);

  function escapeRe(s) { return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'); }

  // Split text into [{ t, hit }] segments around the search terms.
  function segments(text, ts) {
    if (!text) return [];
    if (!ts.length) return [{ t: text, hit: false }];
    const re = new RegExp('(' + ts.map(escapeRe).join('|') + ')', 'ig');
    const out = [];
    let last = 0, m;
    while ((m = re.exec(text))) {
      if (m.index > last) out.push({ t: text.slice(last, m.index), hit: false });
      out.push({ t: m[0], hit: true });
      last = m.index + m[0].length;
      if (re.lastIndex === m.index) re.lastIndex++;
    }
    if (last < text.length) out.push({ t: text.slice(last), hit: false });
    return out;
  }

  // A content excerpt centred on the first matched term.
  function snippet(content, ts, radius = 60) {
    if (!content) return '';
    const flat = content.replace(/\s+/g, ' ').trim();
    const lc = flat.toLowerCase();
    let idx = -1;
    for (const t of ts) { const i = lc.indexOf(t); if (i >= 0 && (idx < 0 || i < idx)) idx = i; }
    if (idx < 0) return flat.slice(0, radius * 2) + (flat.length > radius * 2 ? '…' : '');
    const start = Math.max(0, idx - radius);
    const end = Math.min(flat.length, idx + radius);
    return (start > 0 ? '…' : '') + flat.slice(start, end) + (end < flat.length ? '…' : '');
  }

  // ── Build the grouped, navigable item list ────────────────────────────────
  function go(hash) { close(); window.location.hash = hash; }

  async function exportCatalog() {
    close();
    try {
      const { blob, filename } = await apiExportZip();
      downloadBlob(blob, filename);
    } catch (e) {
      alert(e?.error || e?.message || 'Export failed');
    }
  }

  function openClass(path) {
    taxonomyFilter.set(path);
    searchMode.set(false);
    searchQuery.set('');
    graphView.set(false);
    close();
    if ($docsMode) openDocsRecord(null);
    else window.location.hash = '/';
    loadMemories();
  }

  $: groups = buildGroups(query, recent, recordResults, $catalog, $canWrite, $docsMode);
  $: flatItems = groups.flatMap(g => g.items);
  $: if (activeIndex > flatItems.length - 1) activeIndex = Math.max(0, flatItems.length - 1);

  function buildGroups(q, recentList, recs, cat, writable, docs) {
    const term = q.trim().toLowerCase();
    const groups = [];

    // On the docs corpus the palette is a search box and nothing more: every
    // command below either writes, or reads an endpoint the public mirror does
    // not serve. Classes and Records carry the whole feature there.
    const cmds = [];
    if (!docs) {
      if (writable) cmds.push({ kind: 'cmd', label: 'New record', run: () => go('/new') });
      cmds.push({ kind: 'cmd', label: 'Preview agent context', run: () => go('/agent') });
      cmds.push({ kind: 'cmd', label: 'View agent skills', run: () => go('/skills') });
      cmds.push({ kind: 'cmd', label: 'Export catalog (.zip)', run: exportCatalog });
      cmds.push({ kind: 'cmd', label: 'Open classification map', run: () => { graphView.set(true); go('/'); } });
    }
    const cmdMatch = term ? cmds.filter(c => c.label.toLowerCase().includes(term)) : cmds;
    if (cmdMatch.length) groups.push({ label: 'Commands', items: cmdMatch });

    let classes = cat.map(e => ({ kind: 'class', label: e.path, sub: e.count != null ? String(e.count) : '', run: () => openClass(e.path) }));
    if (term) classes = classes.filter(c => c.label.toLowerCase().includes(term));
    classes = classes.slice(0, term ? 8 : 5);
    if (classes.length) groups.push({ label: 'Classes', items: classes });

    const src = term ? recs : recentList;
    const records = src.map(r => ({
      kind: 'record', label: r.title, sub: r.taxonomy, record: r,
      run: () => { if (docs) { close(); openDocsRecord(r); } else go(`/memory/${r.id}`); },
    }));
    if (records.length) groups.push({ label: term ? 'Records' : 'Recent', items: records });

    return groups;
  }

  // ── Keyboard ──────────────────────────────────────────────────────────────
  function onWindowKey(e) {
    if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
      // Anonymous docs readers hold no key but get the same search.
      if (!$activeKey && !$docsMode) return;
      e.preventDefault();
      paletteOpen.update(v => !v);
    } else if (e.key === 'Escape' && $paletteOpen) {
      close();
    }
  }

  async function onInputKey(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); activeIndex = Math.min(flatItems.length - 1, activeIndex + 1); scrollActive(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); activeIndex = Math.max(0, activeIndex - 1); scrollActive(); }
    else if (e.key === 'Enter') { e.preventDefault(); flatItems[activeIndex]?.run(); }
  }

  async function scrollActive() {
    await tick();
    listEl?.querySelector('.cmdk__item.active')?.scrollIntoView({ block: 'nearest' });
  }

  function indexOf(item) { return flatItems.indexOf(item); }
</script>

<svelte:window on:keydown={onWindowKey} />

{#if $paletteOpen}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="cmdk-overlay" on:click|self={close}>
    <div class="cmdk" role="dialog" aria-label="Command palette">
      <div class="cmdk__input-row">
        <svg class="cmdk__search" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="6.5" cy="6.5" r="4.5"/><path d="M10.5 10.5l3 3"/>
        </svg>
        <!-- svelte-ignore a11y-autofocus -->
        <input
          bind:this={inputEl}
          bind:value={query}
          on:keydown={onInputKey}
          class="cmdk__input"
          placeholder="Search records, jump to a class, or run a command…"
          autocomplete="off"
          spellcheck="false"
        />
        <kbd class="cmdk__esc">esc</kbd>
      </div>

      <div class="cmdk__results" bind:this={listEl}>
        {#each groups as g (g.label)}
          <div class="cmdk__group">{g.label}</div>
          {#each g.items as it (it.kind + it.label)}
            <button
              class="cmdk__item"
              class:active={indexOf(it) === activeIndex}
              on:mousemove={() => activeIndex = indexOf(it)}
              on:click={it.run}
            >
              <span class="cmdk__icon cmdk__icon--{it.kind}">
                {#if it.kind === 'record'}
                  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4"><rect x="3.5" y="2.5" width="9" height="11" rx="1"/><path d="M5.5 5.5h5M5.5 8h5M5.5 10.5h3"/></svg>
                {:else if it.kind === 'class'}
                  <span class="cmdk__dot">·</span>
                {:else}
                  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M8 3v10M3 8h10"/></svg>
                {/if}
              </span>

              <span class="cmdk__body">
                <span class="cmdk__row">
                  <span class="cmdk__label cmdk__label--{it.kind}">
                    {#if it.kind === 'record' && terms.length}{#each segments(it.label, terms) as s}{#if s.hit}<mark>{s.t}</mark>{:else}{s.t}{/if}{/each}{:else}{it.label}{/if}
                  </span>
                  {#if it.sub}<span class="cmdk__sub">{it.sub}</span>{/if}
                </span>
                {#if it.kind === 'record' && terms.length && it.record?.content}
                  <span class="cmdk__snippet">
                    {#each segments(snippet(it.record.content, terms), terms) as s}{#if s.hit}<mark>{s.t}</mark>{:else}{s.t}{/if}{/each}
                  </span>
                {/if}
              </span>
            </button>
          {/each}
        {/each}

        {#if flatItems.length === 0}
          <div class="cmdk__empty">No matches for "{query.trim()}"</div>
        {/if}
      </div>

      <div class="cmdk__footer">
        <span><kbd>↑</kbd><kbd>↓</kbd> navigate</span>
        <span><kbd>↵</kbd> open</span>
        <span><kbd>esc</kbd> close</span>
      </div>
    </div>
  </div>
{/if}

<style>
  .cmdk-overlay {
    position: fixed; inset: 0; z-index: 200;
    background: var(--overlay);
    backdrop-filter: blur(2px);
    display: flex; justify-content: center;
    padding: 12vh 16px 16px;
  }
  .cmdk {
    width: 100%; max-width: 600px; max-height: 64vh;
    display: flex; flex-direction: column;
    background: var(--bg-raised);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-lg);
    overflow: hidden;
  }

  .cmdk__input-row {
    display: flex; align-items: center; gap: 10px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
  }
  .cmdk__search { width: 16px; height: 16px; color: var(--text-3); flex-shrink: 0; }
  .cmdk__input {
    flex: 1; border: none; background: transparent; padding: 0; width: auto;
    font-family: var(--font); font-size: 16px; color: var(--text);
  }
  .cmdk__input:focus { box-shadow: none; }
  .cmdk__esc {
    font-family: var(--font-mono); font-size: 10px; color: var(--text-3);
    border: 1px solid var(--border-mid); border-radius: var(--radius);
    padding: 2px 6px; flex-shrink: 0;
  }

  .cmdk__results { overflow-y: auto; padding: 6px; flex: 1; }
  .cmdk__group {
    font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .12em; text-transform: uppercase;
    color: var(--text-3); padding: 10px 10px 5px;
  }
  .cmdk__item {
    display: flex; align-items: flex-start; gap: 11px; width: 100%;
    padding: 8px 10px; border: none; background: transparent;
    border-radius: var(--radius); cursor: pointer; text-align: left;
  }
  .cmdk__item.active { background: var(--accent-wash); }

  .cmdk__icon {
    width: 22px; height: 22px; flex-shrink: 0; margin-top: 1px;
    display: flex; align-items: center; justify-content: center;
    border-radius: var(--radius); color: var(--text-2);
  }
  .cmdk__icon svg { width: 14px; height: 14px; }
  .cmdk__icon--record { background: var(--bg-subtle); color: var(--text-2); }
  .cmdk__icon--cmd { background: var(--accent-wash); color: var(--accent); }
  .cmdk__icon--class { color: var(--accent); }
  .cmdk__dot { font-family: var(--font-mono); font-size: 18px; line-height: 1; color: var(--accent); }

  .cmdk__body { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .cmdk__row { display: flex; align-items: baseline; gap: 10px; min-width: 0; }

  .cmdk__label {
    flex: 1; min-width: 0;
    font-family: var(--font-display); font-size: 15px; color: var(--text);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .cmdk__label--class, .cmdk__label--cmd { font-family: var(--font-mono); font-size: 13px; }
  .cmdk__label mark { background: color-mix(in srgb, var(--accent) 16%, transparent); color: var(--accent-hover); border-radius: 2px; }

  .cmdk__sub {
    font-family: var(--font-mono); font-size: 11px; color: var(--text-3);
    white-space: nowrap; flex-shrink: 0; max-width: 40%; overflow: hidden; text-overflow: ellipsis;
  }

  .cmdk__snippet {
    font-size: 12.5px; line-height: 1.45; color: var(--text-3);
    display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
  }
  .cmdk__snippet mark { background: color-mix(in srgb, var(--accent) 14%, transparent); color: var(--text-2); border-radius: 2px; }

  .cmdk__empty {
    padding: 28px 12px; text-align: center;
    font-size: 14px; font-style: italic; color: var(--text-3);
  }

  .cmdk__footer {
    display: flex; gap: 16px;
    padding: 9px 16px;
    border-top: 1px solid var(--border);
    font-family: var(--font-mono); font-size: 10.5px; color: var(--text-3);
  }
  .cmdk__footer kbd {
    font-family: var(--font-mono); font-size: 10px; color: var(--text-2);
    border: 1px solid var(--border-mid); border-radius: 3px;
    padding: 0 4px; margin-right: 2px;
  }

  @media (max-width: 560px) {
    .cmdk-overlay { padding: 8vh 10px 10px; }
    .cmdk__footer { display: none; }
  }
</style>
