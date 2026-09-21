<script>
  import { onMount } from 'svelte';
  import { catalog } from '../lib/store.js';
  import { apiMemories, apiCatalog, apiSearch } from '../lib/api.js';
  import { buildTree, debounce } from '../lib/utils.js';

  // Mirror the server's token math exactly: CountTokens(s) = ceil(runes/4),
  // and a scoped entry costs CountTokens(id)+CountTokens(title)+CountTokens(taxonomy).
  const tok = (s) => (s ? Math.floor(([...s].length + 3) / 4) : 0);
  const cost = (m) => tok(m.id) + tok(m.title) + tok(m.taxonomy);

  let selected = '';
  let budget = 2000;            // scope's default agent budget
  let items = [];               // newest-first scope manifest
  let loading = false;

  // ── Search-within-scope ─────────────────────────────────────────────────────
  let query = '';
  let searchItems = [];
  let searching = false;
  const runSearch = debounce(async (q, tax) => {
    const t = q.trim();
    if (!t) { searchItems = []; searching = false; return; }
    searching = true;
    try { searchItems = (await apiSearch(t, tax)).results || []; }
    catch (_) { searchItems = []; }
    finally { searching = false; }
  }, 200);
  $: runSearch(query, selected);

  $: searchMode = !!query.trim();
  $: source = searchMode ? searchItems : items;
  $: busy = searchMode ? searching : loading;

  // Flatten the catalog into selectable scopes (roots + leaves).
  $: options = buildTree($catalog).flatMap(r => [
    { value: r.path, label: r.path, depth: 0, count: r.count },
    ...r.children.map(c => ({ value: c.path, label: c.path, depth: 1, count: c.count })),
  ]);

  // Greedy budget walk — identical to MemoryDB.Scope: keep entries that fit,
  // skip (but keep scanning past) ones that don't.
  $: sim = (() => {
    let used = 0; const inc = []; const omit = [];
    for (const m of source) {
      const c = cost(m);
      if (budget > 0 && used + c > budget) omit.push({ m, c });
      else { used += c; inc.push({ m, c }); }
    }
    const total = inc.reduce((a, x) => a + x.c, 0) + omit.reduce((a, x) => a + x.c, 0);
    return { inc, omit, used, total };
  })();

  $: pct = budget > 0 ? Math.min(100, Math.round((sim.used / budget) * 100)) : 0;

  // ── Highlighting (search mode) ──────────────────────────────────────────────
  $: terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  function escapeRe(s) { return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'); }
  function segments(text, ts) {
    if (!text) return [];
    if (!ts.length) return [{ t: text, hit: false }];
    const re = new RegExp('(' + ts.map(escapeRe).join('|') + ')', 'ig');
    const out = []; let last = 0, m;
    while ((m = re.exec(text))) {
      if (m.index > last) out.push({ t: text.slice(last, m.index), hit: false });
      out.push({ t: m[0], hit: true });
      last = m.index + m[0].length;
      if (re.lastIndex === m.index) re.lastIndex++;
    }
    if (last < text.length) out.push({ t: text.slice(last), hit: false });
    return out;
  }
  function snippet(content, ts, radius = 70) {
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

  async function loadItems() {
    if (!selected) { items = []; return; }
    loading = true;
    try { items = (await apiMemories({ taxonomy: selected, limit: 500 })).memories || []; }
    catch (_) { items = []; }
    finally { loading = false; }
  }

  $: if (selected) loadItems();

  onMount(async () => {
    if ($catalog.length === 0) {
      try { catalog.set((await apiCatalog()).taxonomy || []); } catch (_) {}
    }
    if (!selected && options.length) selected = options[0].value;
  });

  // Pick a sensible default once options arrive.
  $: if (!selected && options.length) selected = options[0].value;

  function goBack() { window.location.hash = '/'; }
  function openRecord(id) { window.location.hash = `/memory/${id}`; }
</script>

<div class="agent-page">
  <header class="agent-header">
    <button class="back-btn" on:click={goBack}>
      <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9 2L4 7l5 5"/></svg>
      Catalog
    </button>
  </header>

  <div class="agent-scroll">
    <div class="agent-body">
      <div class="agent-eyebrow">Agent context preview</div>
      <h1 class="agent-title">What an agent sees</h1>
      <p class="agent-intro">
        When an agent scopes to a class it receives this manifest — record titles within its
        token budget, newest first. Type a query to watch what it pulls instead when it
        <em>searches</em> the scope. Records that overflow the budget are dropped until the agent
        narrows or the budget grows.
      </p>

      <!-- Controls -->
      <div class="controls">
        <label class="control">
          <span class="control__label">Class</span>
          <select bind:value={selected}>
            {#each options as o (o.value)}
              <option value={o.value}>{o.depth ? '— ' : ''}{o.label} ({o.count})</option>
            {/each}
          </select>
        </label>

        <label class="control control--budget">
          <span class="control__label">Token budget</span>
          <div class="budget-input">
            <input type="range" min="200" max="8000" step="100" bind:value={budget} />
            <input class="budget-num" type="number" min="0" step="100" bind:value={budget} />
          </div>
        </label>
      </div>

      <!-- Search within scope -->
      <label class="control control--search">
        <span class="control__label">Search within scope <span class="control__opt">optional</span></span>
        <div class="search-field">
          <svg class="search-field__icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
            <circle cx="6.5" cy="6.5" r="4.5"/><path d="M10.5 10.5l3 3"/>
          </svg>
          <input
            class="search-field__input input-mono"
            type="text"
            bind:value={query}
            placeholder="e.g. refresh token rotation"
            autocomplete="off"
            spellcheck="false"
          />
          {#if query}
            <button class="search-field__clear" on:click={() => query = ''} title="Clear">✕</button>
          {/if}
        </div>
      </label>

      <!-- Mode line -->
      <div class="mode-line">
        {#if searchMode}
          <span class="mode-line__tag mode-line__tag--search">search</span>
          ranked matches for <code>{query.trim()}</code> in <code>{selected || '—'}</code>
        {:else}
          <span class="mode-line__tag">scope</span>
          newest-first manifest of <code>{selected || '—'}</code>
        {/if}
      </div>

      <!-- Meter -->
      <div class="meter">
        <div class="meter__bar"><div class="meter__fill" class:full={sim.used >= budget && sim.omit.length} style="width:{pct}%"></div></div>
        <div class="meter__legend">
          <span class="meter__used">{sim.used.toLocaleString()} <span class="meter__unit">/ {budget.toLocaleString()} tokens</span></span>
          <span class="meter__count">{sim.inc.length} of {sim.inc.length + sim.omit.length} records in context</span>
        </div>
      </div>

      <!-- Manifest -->
      {#if busy && source.length === 0}
        <div class="agent-empty"><div class="spinner"></div></div>
      {:else if source.length === 0}
        <div class="agent-empty">
          <p>
            {#if searchMode}No matches for <code>{query.trim()}</code> in <code>{selected || '—'}</code>.
            {:else}No records under <code>{selected || '—'}</code> yet.{/if}
          </p>
        </div>
      {:else}
        <div class="manifest__head">
          <span class="manifest__label">In context</span>
          <span class="manifest__num">{sim.inc.length}</span>
        </div>
        <ul class="manifest">
          {#each sim.inc as { m, c } (m.id)}
            <li class="entry" on:click={() => openRecord(m.id)}>
              <div class="entry__main">
                <span class="entry__call">{m.taxonomy || '—'}</span>
                <span class="entry__title">
                  {#if searchMode && terms.length}{#each segments(m.title, terms) as s}{#if s.hit}<mark>{s.t}</mark>{:else}{s.t}{/if}{/each}{:else}{m.title}{/if}
                </span>
                <span class="entry__tok">{c} tok</span>
              </div>
              {#if searchMode && terms.length && m.content}
                <span class="entry__snippet">{#each segments(snippet(m.content, terms), terms) as s}{#if s.hit}<mark>{s.t}</mark>{:else}{s.t}{/if}{/each}</span>
              {/if}
            </li>
          {/each}
        </ul>

        {#if sim.omit.length}
          <div class="budget-line"><span>budget spent</span></div>
          <div class="manifest__head manifest__head--omit">
            <span class="manifest__label">Omitted · over budget</span>
            <span class="manifest__num">{sim.omit.length}</span>
          </div>
          <ul class="manifest manifest--omit">
            {#each sim.omit as { m, c } (m.id)}
              <li class="entry entry--omit" on:click={() => openRecord(m.id)}>
                <div class="entry__main">
                  <span class="entry__call">{m.taxonomy || '—'}</span>
                  <span class="entry__title">{m.title}</span>
                  <span class="entry__tok">{c} tok</span>
                </div>
              </li>
            {/each}
          </ul>
        {/if}
      {/if}
    </div>
  </div>
</div>

<style>
  .agent-page { height: 100%; display: flex; flex-direction: column; background: var(--bg); overflow: hidden; }

  .agent-header {
    height: var(--toolbar-h); display: flex; align-items: center;
    padding: 0 20px; border-bottom: 1px solid var(--border); flex-shrink: 0;
  }
  .back-btn {
    display: inline-flex; align-items: center; gap: 5px; padding: 4px 8px;
    background: transparent; border: none; border-radius: var(--radius);
    color: var(--text-2); font-family: var(--font); font-size: 13px; cursor: pointer;
    transition: color var(--transition), background var(--transition);
  }
  .back-btn svg { width: 11px; height: 11px; }
  .back-btn:hover { color: var(--text); background: var(--bg-hover); }

  .agent-scroll { flex: 1; overflow-y: auto; }
  .agent-body { padding: 40px 48px 80px; max-width: 820px; width: 100%; margin: 0 auto; }
  @media (max-width: 700px) { .agent-body { padding: 24px 18px 60px; } }

  .agent-eyebrow {
    font-family: var(--font-mono); font-size: 10.5px; font-weight: 500;
    letter-spacing: .14em; text-transform: uppercase; color: var(--text-3); margin-bottom: 12px;
  }
  .agent-title {
    font-family: var(--font-display); font-size: 2.3em; font-weight: 520;
    line-height: 1.12; letter-spacing: -.02em; margin: 0 0 14px; color: var(--text);
  }
  @media (max-width: 560px) { .agent-title { font-size: 1.9em; } }
  .agent-intro { font-size: 15.5px; line-height: 1.6; color: var(--text-2); max-width: 62ch; margin: 0 0 28px; }
  .agent-intro em { font-style: italic; color: var(--text); }

  /* Controls */
  .controls { display: flex; gap: 20px; flex-wrap: wrap; margin-bottom: 18px; }
  .control { display: flex; flex-direction: column; gap: 7px; }
  .control--budget { flex: 1; min-width: 240px; }
  .control--search { margin-bottom: 18px; }
  .control__label {
    font-family: var(--font-mono); font-size: 10px; font-weight: 500;
    letter-spacing: .1em; text-transform: uppercase; color: var(--text-2);
  }
  .control__opt { color: var(--text-3); letter-spacing: .06em; }
  .control select { font-family: var(--font-mono); font-size: 13px; min-width: 220px; }
  .budget-input { display: flex; align-items: center; gap: 12px; }
  .budget-input input[type="range"] { flex: 1; accent-color: var(--accent); cursor: pointer; padding: 0; }
  .budget-num { width: 92px; font-family: var(--font-mono); font-size: 13px; text-align: right; }

  .search-field { position: relative; display: flex; align-items: center; }
  .search-field__icon {
    position: absolute; left: 11px; width: 14px; height: 14px; color: var(--text-3); pointer-events: none;
  }
  .search-field__input { padding-left: 34px; padding-right: 30px; }
  .search-field__clear {
    position: absolute; right: 8px; width: 20px; height: 20px; padding: 0;
    background: transparent; border: none; color: var(--text-3); cursor: pointer; font-size: 12px;
    border-radius: var(--radius);
  }
  .search-field__clear:hover { color: var(--text); background: var(--bg-hover); }

  /* Mode line */
  .mode-line {
    display: flex; align-items: center; gap: 7px; flex-wrap: wrap;
    font-size: 13px; color: var(--text-3); margin-bottom: 18px;
  }
  .mode-line code { font-family: var(--font-mono); font-size: 12px; color: var(--accent); }
  .mode-line__tag {
    font-family: var(--font-mono); font-size: 9px; letter-spacing: .12em; text-transform: uppercase;
    color: var(--text-2); background: var(--bg-subtle); border: 1px solid var(--border);
    border-radius: 99px; padding: 2px 8px;
  }
  .mode-line__tag--search { color: var(--accent); background: var(--accent-wash); border-color: color-mix(in srgb, var(--accent) 30%, transparent); }

  /* Meter */
  .meter { margin-bottom: 30px; }
  .meter__bar { height: 8px; background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 99px; overflow: hidden; }
  .meter__fill { height: 100%; background: var(--accent); border-radius: 99px; transition: width 120ms ease; }
  .meter__fill.full { background: var(--stamp); }
  .meter__legend { display: flex; justify-content: space-between; align-items: baseline; margin-top: 9px; gap: 12px; }
  .meter__used { font-family: var(--font-mono); font-size: 14px; color: var(--text); font-variant-numeric: tabular-nums; }
  .meter__unit { color: var(--text-3); font-size: 12px; }
  .meter__count { font-family: var(--font-mono); font-size: 11.5px; color: var(--text-3); text-align: right; }

  /* Manifest */
  .manifest__head { display: flex; align-items: baseline; gap: 8px; padding: 6px 4px; }
  .manifest__label {
    font-family: var(--font-mono); font-size: 10px; letter-spacing: .12em; text-transform: uppercase; color: var(--text-3);
  }
  .manifest__head--omit .manifest__label { color: var(--stamp); }
  .manifest__num {
    font-family: var(--font-mono); font-size: 10px; color: var(--text-3);
    background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 99px; padding: 0 6px;
  }

  .manifest { list-style: none; margin: 0 0 8px; padding: 0; }
  .entry {
    padding: 10px 6px; border-bottom: 1px solid var(--border); border-left: 2px solid transparent;
    cursor: pointer; transition: background var(--transition), border-color var(--transition);
  }
  .entry:hover { background: var(--bg-hover); border-left-color: var(--accent); }
  .entry__main { display: grid; grid-template-columns: 190px 1fr auto; align-items: baseline; gap: 18px; }
  .entry__call {
    font-family: var(--font-mono); font-size: 11.5px; color: var(--accent);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .entry__title {
    font-family: var(--font-display); font-size: 16px; color: var(--text);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .entry__title mark { background: color-mix(in srgb, var(--accent) 16%, transparent); color: var(--accent-hover); border-radius: 2px; }
  .entry__tok { font-family: var(--font-mono); font-size: 11px; color: var(--text-3); white-space: nowrap; font-variant-numeric: tabular-nums; }

  .entry__snippet {
    display: block; margin-top: 4px; padding-right: 60px;
    font-size: 13px; line-height: 1.45; color: var(--text-3);
    display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
  }
  .entry__snippet mark { background: color-mix(in srgb, var(--accent) 14%, transparent); color: var(--text-2); border-radius: 2px; }

  .entry--omit { opacity: .5; }
  .entry--omit:hover { opacity: .75; border-left-color: var(--stamp); }

  .budget-line {
    display: flex; align-items: center; gap: 12px; margin: 18px 4px 10px;
    color: var(--stamp);
  }
  .budget-line::before, .budget-line::after { content: ''; flex: 1; height: 0; border-top: 1px dashed color-mix(in srgb, var(--stamp) 30%, transparent); }
  .budget-line span {
    font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .14em; text-transform: uppercase;
  }

  @media (max-width: 560px) {
    .entry__main { grid-template-columns: 1fr auto; gap: 4px 14px; }
    .entry__call { grid-column: 1 / -1; order: -1; }
    .entry__snippet { padding-right: 0; }
  }

  .agent-empty {
    display: flex; align-items: center; justify-content: center;
    min-height: 160px; color: var(--text-3); font-size: 14px;
  }
  .agent-empty code { font-family: var(--font-mono); color: var(--accent); }
</style>
