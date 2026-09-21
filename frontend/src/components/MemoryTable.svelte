<script>
  import { memories, loading, memoryTotal, searchMode, docsMode } from '../lib/store.js';
  import { loadMemories } from '../lib/data.js';
  import { formatDate, PAGE_SIZE } from '../lib/utils.js';
  import { openDocsRecord } from '../lib/docs-route.js';

  // Same row, two destinations: the editor in the app, the read-only reader on
  // the public docs corpus, which routes on the path so its URLs are shareable.
  function openEditor(mem) {
    if ($docsMode) openDocsRecord(mem);
    else window.location.hash = `/memory/${mem.id}`;
  }

  $: allLoaded = $searchMode || $memories.length >= $memoryTotal;
  $: showLoadMore = !allLoaded && !$loading;

  let loadingMore = false;
  async function loadMore() {
    loadingMore = true;
    await loadMemories(true);
    loadingMore = false;
  }
</script>

<div class="index-wrap">
  {#if $loading && $memories.length === 0}
    <div class="loading-state"><div class="spinner"></div></div>
  {:else if $memories.length === 0}
    <div class="empty-state">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4">
        <rect x="4" y="3" width="16" height="18" rx="1.5"/>
        <path d="M8 8h8M8 12h8M8 16h5"/>
      </svg>
      <p class="empty-title">The catalog is empty</p>
      <p class="empty-sub">
        {$docsMode ? 'No records are published here yet.' : 'New records will be indexed here once written.'}
      </p>
    </div>
  {:else}
    <ol class="index">
      {#each $memories as m (m.id)}
        <li class="record" on:click={() => openEditor(m)}>
          <span class="record__call" title={m.taxonomy}>{m.taxonomy || '—'}</span>
          <span class="record__title" title={m.title}>{m.title}</span>
          <span class="record__date">{formatDate(m.updated_at)}</span>
        </li>
      {/each}
    </ol>

    <div class="index-footer">
      {#if showLoadMore}
        <button class="load-more-btn" on:click={loadMore} disabled={loadingMore}>
          {#if loadingMore}
            <div class="spinner spinner--sm"></div>
            Pulling records…
          {:else}
            Pull {PAGE_SIZE} more
          {/if}
        </button>
      {:else if !$loading}
        <span class="all-loaded">End of catalog · {$memoryTotal} record{$memoryTotal !== 1 ? 's' : ''}</span>
      {/if}
    </div>
  {/if}
</div>

<style>
  .index-wrap { flex: 1; overflow-y: auto; display: flex; flex-direction: column; }

  .index {
    list-style: none;
    margin: 0;
    padding: 0;
    max-width: 1100px;
    width: 100%;
  }

  .record {
    display: grid;
    grid-template-columns: 200px 1fr auto;
    align-items: baseline;
    gap: 20px;
    padding: 14px 28px 14px 26px;
    border-bottom: 1px solid var(--border);
    border-left: 2px solid transparent;
    cursor: pointer;
    transition: background var(--transition), border-color var(--transition);
  }
  .record:hover { background: var(--bg-hover); border-left-color: var(--accent); }

  .record__call {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--accent);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    letter-spacing: .01em;
  }

  .record__title {
    font-family: var(--font-display);
    font-size: 17px;
    font-weight: 480;
    line-height: 1.35;
    color: var(--text);
    letter-spacing: -.005em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .record:hover .record__title { color: var(--accent-hover); }

  .record__date {
    font-family: var(--font-mono);
    font-size: 11.5px;
    color: var(--text-3);
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }

  @media (max-width: 640px) {
    .record {
      grid-template-columns: 1fr auto;
      gap: 4px 16px;
      padding: 13px 18px;
    }
    .record__call { grid-column: 1 / -1; order: -1; font-size: 11px; }
    .record__title { font-size: 16px; }
  }

  /* ── Footer ──────────────────────────────────────────────────────────────── */
  .index-footer { display: flex; justify-content: center; padding: 24px 16px 40px; flex-shrink: 0; }
  .load-more-btn {
    display: inline-flex; align-items: center; gap: 7px;
    padding: 8px 22px;
    background: transparent;
    border: 1px solid var(--border-mid);
    border-radius: var(--radius);
    color: var(--text-2);
    font-family: var(--font);
    font-size: 14px;
    cursor: pointer;
    transition: color var(--transition), border-color var(--transition), background var(--transition);
  }
  .load-more-btn:hover:not(:disabled) { color: var(--text); border-color: var(--text-3); background: var(--bg-hover); }
  .load-more-btn:disabled { opacity: .5; cursor: not-allowed; }

  .all-loaded {
    font-family: var(--font-mono);
    font-size: 11px;
    letter-spacing: .08em;
    text-transform: uppercase;
    color: var(--text-3);
  }

  .spinner--sm { width: 13px; height: 13px; border-width: 1.5px; }

  /* ── States ──────────────────────────────────────────────────────────────── */
  .empty-state, .loading-state {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    gap: 6px; height: 360px; color: var(--text-3); text-align: center;
  }
  .empty-state svg { width: 38px; height: 38px; stroke: var(--text-3); margin-bottom: 8px; }
  .empty-title { font-family: var(--font-display); font-size: 19px; font-weight: 500; color: var(--text-2); margin: 0; }
  .empty-sub { font-size: 14px; color: var(--text-3); margin: 0; }
</style>
