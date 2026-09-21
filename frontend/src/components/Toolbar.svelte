<script>
  import { createEventDispatcher } from 'svelte';
  import { memories, memoryTotal, memoryOffset, canWrite, paletteOpen } from '../lib/store.js';
  import { PAGE_SIZE } from '../lib/utils.js';

  const dispatch = createEventDispatcher();
  const modKey = typeof navigator !== 'undefined' && /mac/i.test(navigator.platform) ? '⌘' : 'Ctrl';

  $: countLabel = $memoryTotal <= PAGE_SIZE || $memoryOffset + $memories.length >= $memoryTotal
    ? `${$memoryTotal} record${$memoryTotal !== 1 ? 's' : ''}`
    : `${$memories.length} of ${$memoryTotal}`;
</script>

<div class="toolbar">
  <button class="mobile-menu-btn btn-icon" aria-label="Open menu" on:click={() => dispatch('toggleSidebar')}>
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
      <path d="M2 4h12M2 8h12M2 12h12"/>
    </svg>
  </button>

  <div class="toolbar__heading">
    <h1 class="toolbar__title">Index</h1>
    <span class="count-label">{countLabel}</span>
  </div>

  <div class="toolbar__right">
    <button class="search-trigger" on:click={() => paletteOpen.set(true)}>
      <svg class="search-trigger__icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="6.5" cy="6.5" r="4.5"/><path d="M10.5 10.5l3 3"/>
      </svg>
      <span class="search-trigger__text">Search the catalog…</span>
      <kbd class="search-trigger__kbd">{modKey} K</kbd>
    </button>
    {#if $canWrite}
      <button class="btn-primary" on:click={() => dispatch('newMemory')}>
        <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M7 2v10M2 7h10"/></svg>
        New record
      </button>
    {/if}
  </div>
</div>

<style>
  .toolbar {
    height: var(--toolbar-h);
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 0 26px;
    border-bottom: 1px solid var(--border-mid);
    background: var(--bg);
    flex-shrink: 0;
  }
  .mobile-menu-btn { display: none; }
  @media (max-width: 768px) { .mobile-menu-btn { display: inline-flex; } }

  .toolbar__heading { flex: 1; display: flex; align-items: baseline; gap: 12px; min-width: 0; }
  .toolbar__title {
    font-family: var(--font-display);
    font-size: 24px;
    font-weight: 540;
    letter-spacing: -.015em;
    color: var(--text);
    margin: 0;
  }

  .toolbar__right { display: flex; align-items: center; gap: 10px; flex-shrink: 0; }

  .count-label {
    font-family: var(--font-mono);
    font-size: 11px;
    letter-spacing: .06em;
    color: var(--text-3);
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }

  /* Search-styled button that opens the ⌘K palette */
  .search-trigger {
    display: inline-flex; align-items: center; gap: 9px;
    height: 36px; width: 260px; padding: 0 10px 0 12px;
    background: var(--bg-raised);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius);
    cursor: pointer; text-align: left;
    transition: border-color var(--transition), background var(--transition);
  }
  .search-trigger:hover { border-color: var(--text-3); background: var(--bg-hover); }
  .search-trigger__icon { width: 14px; height: 14px; color: var(--text-3); flex-shrink: 0; }
  .search-trigger__text {
    flex: 1; font-family: var(--font); font-size: 13.5px; color: var(--text-3);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .search-trigger__kbd {
    font-family: var(--font-mono); font-size: 10px; letter-spacing: .04em;
    color: var(--text-3);
    border: 1px solid var(--border-mid); border-radius: 3px;
    padding: 1px 5px; flex-shrink: 0;
  }

  .btn-primary svg { width: 13px; height: 13px; }

  @media (max-width: 600px) {
    .toolbar { padding: 0 16px; }
    .count-label { display: none; }
    /* Collapse to an icon-only search button */
    .search-trigger { width: 36px; padding: 0; justify-content: center; }
    .search-trigger__text, .search-trigger__kbd { display: none; }
  }
</style>
