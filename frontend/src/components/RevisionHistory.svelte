<script>
  import { onMount, createEventDispatcher } from 'svelte';
  import { canWrite } from '../lib/store.js';
  import { apiMemoryHistory, apiRestoreRevision } from '../lib/api.js';
  import { formatFullDate } from '../lib/utils.js';
  import MarkdownPreview from './MarkdownPreview.svelte';

  export let memoryId;
  export let currentVersion = 0; // the live version, so we can mark it "current"

  const dispatch = createEventDispatcher();

  let revisions = [];
  let loading = true;
  let error = '';
  let selected = null;   // the revision whose content is expanded
  let restoring = 0;     // version being restored (0 = none)

  onMount(load);

  async function load() {
    loading = true;
    error = '';
    try {
      const data = await apiMemoryHistory(memoryId);
      revisions = data.revisions || [];
      // Default the preview to the newest (current) revision, if any.
      selected = revisions.length ? revisions[0].version : null;
    } catch (err) {
      error = err?.message || err?.error || 'Could not load history';
    } finally {
      loading = false;
    }
  }

  const short = (id) => (id && id.length > 10 ? id.slice(0, 8) + '…' : id);

  // Who made this version. The server resolves the author id to a display name;
  // the shortened id is the fallback for an account the registry no longer
  // knows, which still has to show as something rather than drop out of the
  // audit trail.
  function authorLabel(rev) {
    if (rev.author_name) return rev.author_name;
    if (rev.author_id) return short(rev.author_id);
    return 'unknown author';
  }
  $: selectedRev = revisions.find((r) => r.version === selected) || null;

  function toggle(rev) {
    selected = selected === rev.version ? null : rev.version;
  }

  async function restore(rev) {
    if (!confirm(`Restore version ${rev.version}? This files it forward as a new version — nothing is lost.`)) return;
    restoring = rev.version;
    error = '';
    try {
      const updated = await apiRestoreRevision(memoryId, rev.version);
      dispatch('restored', updated); // parent refreshes the editor + list
      await load();                  // history now has the new head
    } catch (err) {
      error = err?.message || err?.error || 'Restore failed';
    } finally {
      restoring = 0;
    }
  }
</script>

<svelte:window on:keydown={(e) => e.key === 'Escape' && dispatch('close')} />

<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
<div class="scrim" on:click={() => dispatch('close')}></div>
<aside class="drawer" role="dialog" aria-label="Revision history">
  <header class="drawer__head">
    <div>
      <div class="drawer__eyebrow">History</div>
      <h2 class="drawer__title">Revisions</h2>
    </div>
    <button class="close" on:click={() => dispatch('close')} aria-label="Close">×</button>
  </header>

  {#if error}<p class="err">{error}</p>{/if}

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if !revisions.length}
    <p class="muted">No revision history recorded for this record.</p>
  {:else}
    <ol class="rev-list">
      {#each revisions as rev (rev.version)}
        {@const isCurrent = rev.version === currentVersion || rev.version === revisions[0].version}
        <li class="rev" class:open={selected === rev.version}>
          <button class="rev__row" on:click={() => toggle(rev)}>
            <span class="rev__ver">v{rev.version}</span>
            <span class="rev__meta">
              <span class="rev__ttl">{rev.title || 'Untitled'}</span>
              <span class="rev__sub">
                {formatFullDate(rev.created_at)}
                · {authorLabel(rev)}
                {#if rev.agent} · via {rev.agent}{/if}
              </span>
            </span>
            {#if isCurrent}<span class="tag tag--current">current</span>{/if}
            <svg class="chev" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M3 4.5l3 3 3-3"/></svg>
          </button>

          {#if selected === rev.version}
            <div class="rev__body">
              <div class="rev__facts">
                {#if rev.taxonomy}<code>{rev.taxonomy}</code>{/if}
                {#if rev.format && rev.format !== 'markdown'}<span class="fmt">{rev.format}</span>{/if}
              </div>
              <div class="rev__content">
                <MarkdownPreview content={rev.content} format={rev.format} />
              </div>
              {#if !isCurrent && $canWrite}
                <div class="rev__actions">
                  <button class="btn-primary btn-restore" on:click={() => restore(rev)} disabled={restoring === rev.version}>
                    {restoring === rev.version ? 'Restoring…' : `Restore v${rev.version}`}
                  </button>
                </div>
              {/if}
            </div>
          {/if}
        </li>
      {/each}
    </ol>
  {/if}
</aside>

<style>
  .scrim { position: fixed; inset: 0; background: var(--overlay); z-index: 60; }
  .drawer {
    position: fixed; z-index: 61; top: 0; right: 0; bottom: 0;
    width: min(460px, calc(100vw - 24px));
    background: var(--bg-raised); border-left: 1px solid var(--border-mid);
    box-shadow: var(--shadow-lg, var(--shadow-md));
    display: flex; flex-direction: column; overflow-y: auto; padding: 18px 20px 40px;
  }
  .drawer__head { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 14px; }
  .drawer__eyebrow { font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .14em; text-transform: uppercase; color: var(--text-3); }
  .drawer__title { font-family: var(--font-display); font-size: 20px; margin: 2px 0 0; color: var(--text); }
  .close { background: none; border: none; font-size: 22px; line-height: 1; color: var(--text-3); cursor: pointer; padding: 0 4px; }
  .close:hover { color: var(--text); }

  .err { color: var(--stamp); font-size: 12.5px; margin: 0 0 10px; background: var(--status-rejected-bg); padding: 6px 9px; border-radius: var(--radius); }
  .muted { color: var(--text-3); font-size: 13px; }

  .rev-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .rev { border: 1px solid transparent; border-radius: var(--radius-md); }
  .rev.open { border-color: var(--border); background: var(--bg-subtle); }

  .rev__row {
    display: flex; align-items: center; gap: 10px; width: 100%;
    padding: 9px 10px; background: transparent; border: none;
    border-radius: var(--radius-md); cursor: pointer; text-align: left;
    transition: background var(--transition);
  }
  .rev:not(.open) .rev__row:hover { background: var(--bg-hover); }
  .rev__ver {
    flex-shrink: 0; font-family: var(--font-mono); font-size: 11px; font-weight: 600;
    color: var(--accent); min-width: 30px;
  }
  .rev__meta { flex: 1; min-width: 0; display: flex; flex-direction: column; line-height: 1.3; }
  .rev__ttl { font-size: 13.5px; color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .rev__sub { font-family: var(--font-mono); font-size: 10px; color: var(--text-3); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .chev { width: 12px; height: 12px; color: var(--text-3); flex-shrink: 0; transition: transform var(--transition); }
  .rev.open .chev { transform: rotate(180deg); }

  .tag { flex-shrink: 0; font-family: var(--font-mono); font-size: 9px; letter-spacing: .08em; text-transform: uppercase; padding: 2px 7px; border-radius: 99px; }
  .tag--current { color: var(--status-pending-text); background: var(--status-pending-bg); }

  .rev__body { padding: 2px 12px 12px; border-top: 1px solid var(--border); margin: 0 2px; }
  .rev__facts { display: flex; gap: 8px; align-items: center; margin: 10px 0; }
  .rev__facts code { font-family: var(--font-mono); font-size: 11px; color: var(--accent); background: var(--bg); padding: 2px 8px; border-radius: var(--radius); border: 1px solid var(--border); }
  .rev__facts .fmt { font-family: var(--font-mono); font-size: 10px; text-transform: uppercase; letter-spacing: .06em; color: var(--text-3); }
  .rev__content {
    background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius-md);
    padding: 14px 16px; max-height: 340px; overflow: auto; font-size: 13.5px;
  }
  .rev__actions { display: flex; justify-content: flex-end; margin-top: 12px; }
  .btn-restore { padding: 5px 14px; font-size: 12.5px; }
</style>
