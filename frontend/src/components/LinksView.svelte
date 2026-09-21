<script>
  import { onMount } from 'svelte';
  import { focusLink, canWrite } from '../lib/store.js';
  import { apiEdgeHealth, apiSuggestLinks, apiGetMemory, apiUpdateMemory } from '../lib/api.js';

  let health = null;
  let loading = true;
  let error = '';

  let suggestions = [];
  let suggestLoading = true;
  let linking = '';   // pairKey currently being linked
  let linkedKeys = new Set();

  async function load() {
    loading = true;
    error = '';
    try {
      health = await apiEdgeHealth();
    } catch (e) {
      error = (e && (e.error || e.message)) || 'Could not load link health';
    } finally {
      loading = false;
    }
  }

  async function loadSuggestions() {
    suggestLoading = true;
    try {
      const data = await apiSuggestLinks(30);
      suggestions = data.suggestions || [];
    } catch (_) {
      suggestions = [];
    } finally {
      suggestLoading = false;
    }
  }

  onMount(() => { load(); loadSuggestions(); });

  function keyOf(s) { return s.source.id + '|' + s.target.id; }

  // Accept a suggestion by appending a [[Target]] wiki-link to the source record.
  async function acceptSuggestion(s) {
    const k = keyOf(s);
    if (linking) return;
    linking = k;
    try {
      const src = await apiGetMemory(s.source.id);
      const body = (src.content || '').trimEnd();
      const link = `[[${s.target.title}]]`;
      const content = body ? `${body}\n\nRelated: ${link}` : `Related: ${link}`;
      await apiUpdateMemory(s.source.id, { content });
      linkedKeys = new Set([...linkedKeys, k]);
      // Refresh health counts to reflect the new edge.
      apiEdgeHealth().then(h => health = h).catch(() => {});
    } catch (e) {
      alert((e && (e.error || e.message)) || 'Could not create link');
    } finally {
      linking = '';
    }
  }

  $: visibleSuggestions = suggestions.filter(s => !linkedKeys.has(keyOf(s)));

  function goBack() { window.location.hash = '/'; }

  // Open a source doc and ask the reading view to scroll to / flash the link.
  function openSource(id, target) {
    focusLink.set(target || null);
    window.location.hash = `/memory/${id}`;
  }

  $: dangling = (health && health.top_dangling) || [];
</script>

<div class="links-page">
  <header class="links-header">
    <button class="back-btn" on:click={goBack}>
      <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9 2L4 7l5 5"/></svg>
      Catalog
    </button>
    <button class="refresh-btn" on:click={load} disabled={loading} title="Refresh">
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2v3h-3"/>
      </svg>
      Refresh
    </button>
  </header>

  <div class="links-scroll">
    <div class="links-body">
      <div class="links-eyebrow">Link graph</div>
      <h1 class="links-title">Dangling links</h1>
      <p class="links-intro">
        Wiki-links whose target title doesn't match any record. Some are typos to fix; others are
        forward references that resolve themselves once the target gets written. Open a record to
        jump straight to the link.
      </p>

      {#if loading}
        <p class="links-state">Loading…</p>
      {:else if error}
        <p class="links-state links-state--err">{error}</p>
      {:else if health}
        <!-- Summary stats -->
        <div class="stats">
          <div class="stat">
            <span class="stat__num">{health.total_edges}</span>
            <span class="stat__lbl">total links</span>
          </div>
          <div class="stat stat--ok">
            <span class="stat__num">{health.resolved}</span>
            <span class="stat__lbl">resolved</span>
          </div>
          <div class="stat stat--warn">
            <span class="stat__num">{health.dangling}</span>
            <span class="stat__lbl">dangling</span>
          </div>
          <div class="stat">
            <span class="stat__num">{health.dangling_targets}</span>
            <span class="stat__lbl">missing titles</span>
          </div>
        </div>

        {#if dangling.length === 0}
          <div class="empty">
            <div class="empty__mark">✓</div>
            <p class="empty__text">
              {#if health.total_edges === 0}
                No links anywhere yet. Once records reference each other with <code>[[Title]]</code>, they'll show up here when a target is missing.
              {:else}
                Every link resolves to a record. Nothing dangling.
              {/if}
            </p>
          </div>
        {:else}
          <ul class="targets">
            {#each dangling as d (d.rel + d.target)}
              <li class="target">
                <div class="target__head">
                  <span class="target__title">{d.target}</span>
                  {#if d.rel !== 'relates'}<span class="target__rel">{d.rel}</span>{/if}
                  <span class="target__count">{d.count} record{d.count !== 1 ? 's' : ''}</span>
                </div>
                <ul class="srcs">
                  {#each d.sources || [] as s (s.id)}
                    <li>
                      <button class="src" on:click={() => openSource(s.id, d.target)}>
                        <span class="src__title">{s.title}</span>
                        {#if s.taxonomy}<span class="src__tax">{s.taxonomy}</span>{/if}
                        <svg class="src__arrow" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M5 2l5 5-5 5"/></svg>
                      </button>
                    </li>
                  {/each}
                </ul>
              </li>
            {/each}
          </ul>
          {#if health.dangling_targets > dangling.length}
            <p class="links-note">Showing the {dangling.length} most-referenced of {health.dangling_targets} missing titles.</p>
          {/if}
        {/if}
      {/if}

      <!-- ── Suggested links ── -->
      <div class="suggest-head">
        <div class="links-eyebrow">Suggested links</div>
        <h2 class="suggest-title">Records that look related but aren't linked</h2>
        <p class="links-intro">
          Pairs with strong text overlap and no <code>[[wiki-link]]</code> between them — likely missing
          connections. {#if $canWrite}Accept one to add a link from the first record to the second.{/if}
        </p>
      </div>

      {#if suggestLoading}
        <p class="links-state">Finding candidates…</p>
      {:else if visibleSuggestions.length === 0}
        <div class="empty">
          <div class="empty__mark empty__mark--neutral">✓</div>
          <p class="empty__text">No unlinked look-alikes found. Your related records are well connected.</p>
        </div>
      {:else}
        <ul class="sugg-list">
          {#each visibleSuggestions as s (keyOf(s))}
            <li class="sugg">
              <button class="sugg__node" on:click={() => openSource(s.source.id)}>
                <span class="sugg__node-title">{s.source.title}</span>
                {#if s.source.taxonomy}<span class="sugg__node-tax">{s.source.taxonomy}</span>{/if}
              </button>
              <span class="sugg__link-icon" title="not yet linked">
                <svg viewBox="0 0 24 14" fill="none" stroke="currentColor" stroke-width="1.4">
                  <path d="M8 7H4M16 7h4M9 4.5L6.5 7 9 9.5M15 4.5L17.5 7 15 9.5" stroke-dasharray="2 2"/>
                </svg>
              </span>
              <button class="sugg__node" on:click={() => openSource(s.target.id)}>
                <span class="sugg__node-title">{s.target.title}</span>
                {#if s.target.taxonomy}<span class="sugg__node-tax">{s.target.taxonomy}</span>{/if}
              </button>
              {#if $canWrite}
                <button class="sugg__accept" on:click={() => acceptSuggestion(s)} disabled={!!linking}>
                  {linking === keyOf(s) ? 'Linking…' : 'Link'}
                </button>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  </div>
</div>

<style>
  .links-page { height: 100%; display: flex; flex-direction: column; background: var(--bg); overflow: hidden; }

  .links-header {
    height: var(--toolbar-h); display: flex; align-items: center; gap: 10px;
    padding: 0 20px; border-bottom: 1px solid var(--border); flex-shrink: 0;
  }
  .back-btn, .refresh-btn {
    display: inline-flex; align-items: center; gap: 5px; padding: 4px 8px;
    background: transparent; border: none; border-radius: var(--radius);
    color: var(--text-2); font-family: var(--font); font-size: 13px; cursor: pointer;
    transition: color var(--transition), background var(--transition);
  }
  .back-btn svg, .refresh-btn svg { width: 12px; height: 12px; }
  .back-btn:hover, .refresh-btn:hover { color: var(--text); background: var(--bg-hover); }
  .refresh-btn { margin-left: auto; }
  .refresh-btn:disabled { opacity: .5; cursor: default; }

  .links-scroll { flex: 1; overflow-y: auto; }
  .links-body { padding: 40px 48px 80px; max-width: 820px; width: 100%; margin: 0 auto; }
  @media (max-width: 700px) { .links-body { padding: 24px 18px 60px; } }

  .links-eyebrow {
    font-family: var(--font-mono); font-size: 10.5px; font-weight: 500;
    letter-spacing: .14em; text-transform: uppercase; color: var(--text-3); margin-bottom: 12px;
  }
  .links-title {
    font-family: var(--font-display); font-size: 2.3em; font-weight: 520;
    line-height: 1.12; letter-spacing: -.02em; margin: 0 0 14px; color: var(--text);
  }
  .links-intro { font-size: 15.5px; line-height: 1.6; color: var(--text-2); max-width: 64ch; margin: 0 0 30px; }

  .links-state { font-size: 14px; color: var(--text-3); font-style: italic; }
  .links-state--err { color: var(--stamp, #b4503c); font-style: normal; }
  .links-note { font-family: var(--font-mono); font-size: 11px; color: var(--text-3); margin-top: 18px; }

  /* Summary stats */
  .stats { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 30px; }
  .stat {
    flex: 1; min-width: 110px; padding: 14px 16px;
    background: var(--bg-subtle); border: 1px solid var(--border); border-radius: var(--radius-md);
    display: flex; flex-direction: column; gap: 3px;
  }
  .stat__num { font-family: var(--font-display); font-size: 26px; font-weight: 540; color: var(--text); font-variant-numeric: tabular-nums; }
  .stat__lbl { font-family: var(--font-mono); font-size: 10px; letter-spacing: .1em; text-transform: uppercase; color: var(--text-3); }
  .stat--ok { border-left: 2px solid var(--success); }
  .stat--ok .stat__num { color: var(--success); }
  .stat--warn { border-left: 2px solid var(--warning); }
  .stat--warn .stat__num { color: var(--warning); }

  /* Empty state */
  .empty { display: flex; flex-direction: column; align-items: center; gap: 12px; padding: 48px 20px; text-align: center; }
  .empty__mark {
    width: 44px; height: 44px; border-radius: 50%;
    display: flex; align-items: center; justify-content: center;
    background: var(--state-resolved-bg); color: var(--success); font-size: 22px;
  }
  .empty__text { font-size: 14.5px; color: var(--text-2); max-width: 48ch; margin: 0; line-height: 1.55; }
  .empty code { font-family: var(--font-mono); font-size: .88em; background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 3px; padding: 0 4px; color: var(--accent); }

  /* Dangling targets */
  .targets { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 14px; }
  .target {
    border: 1px solid var(--border); border-radius: var(--radius-md);
    background: var(--bg-raised); border-left: 2px solid var(--warning); overflow: hidden;
  }
  .target__head {
    display: flex; align-items: center; gap: 10px;
    padding: 12px 16px; border-bottom: 1px solid var(--border); background: var(--bg-subtle);
  }
  .target__title { font-family: var(--font-display); font-size: 16px; font-weight: 520; color: var(--text); font-style: italic; }
  .target__rel {
    font-family: var(--font-mono); font-size: 9.5px; font-weight: 600; letter-spacing: .04em; text-transform: uppercase;
    color: var(--stamp, #b4503c); background: var(--status-rejected-bg, #f6e7e3);
    border-radius: 99px; padding: 1px 7px;
  }
  .target__count { margin-left: auto; font-family: var(--font-mono); font-size: 11px; color: var(--text-3); }

  .srcs { list-style: none; margin: 0; padding: 6px; display: flex; flex-direction: column; gap: 2px; }
  .src {
    display: flex; align-items: center; gap: 10px; width: 100%;
    padding: 9px 12px; background: transparent; border: none; border-radius: var(--radius);
    cursor: pointer; text-align: left;
    transition: background var(--transition);
  }
  .src:hover { background: var(--bg-hover); }
  .src__title {
    font-family: var(--font-display); font-size: 14.5px; color: var(--text);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis; flex-shrink: 1;
  }
  .src__tax {
    font-family: var(--font-mono); font-size: 11px; color: var(--accent); opacity: .85;
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .src__arrow { width: 12px; height: 12px; color: var(--text-3); margin-left: auto; flex-shrink: 0; }
  .src:hover .src__arrow { color: var(--accent); }

  /* ── Suggested links ── */
  .suggest-head { margin-top: 56px; padding-top: 30px; border-top: 1px solid var(--border); }
  .suggest-title {
    font-family: var(--font-display); font-size: 1.5em; font-weight: 520; line-height: 1.15;
    letter-spacing: -.01em; margin: 0 0 12px; color: var(--text);
  }
  .empty__mark--neutral { background: var(--bg-subtle); color: var(--text-3); }

  .sugg-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .sugg {
    display: flex; align-items: center; gap: 10px;
    border: 1px solid var(--border); border-radius: var(--radius-md);
    background: var(--bg-raised); padding: 10px 12px;
  }
  .sugg__node {
    flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px;
    background: transparent; border: none; border-radius: var(--radius); padding: 4px 8px;
    text-align: left; cursor: pointer; transition: background var(--transition);
  }
  .sugg__node:hover { background: var(--bg-hover); }
  .sugg__node-title {
    font-family: var(--font-display); font-size: 14px; font-weight: 520; color: var(--text);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .sugg__node-tax {
    font-family: var(--font-mono); font-size: 10.5px; color: var(--accent); opacity: .85;
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .sugg__link-icon { flex-shrink: 0; color: var(--text-3); display: flex; }
  .sugg__link-icon svg { width: 26px; height: 15px; }
  .sugg__accept {
    flex-shrink: 0; padding: 6px 14px; background: var(--accent); border: none;
    border-radius: var(--radius); color: var(--accent-text); font-family: var(--font); font-size: 12.5px;
    cursor: pointer; transition: opacity .15s;
  }
  .sugg__accept:hover:not(:disabled) { opacity: .9; }
  .sugg__accept:disabled { opacity: .5; cursor: default; }
  @media (max-width: 560px) {
    .sugg { flex-wrap: wrap; }
    .sugg__node { flex-basis: 40%; }
  }
</style>
