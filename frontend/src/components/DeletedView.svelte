<script>
  import { onMount } from 'svelte';
  import { currentTeam } from '../lib/store.js';
  import { apiDeletedMemories } from '../lib/api.js';
  import { formatFullDate } from '../lib/utils.js';

  let deleted = [];
  let loading = true;
  let error = '';

  async function load() {
    loading = true;
    error = '';
    try {
      const data = await apiDeletedMemories({ limit: 200 });
      deleted = data.deleted || [];
    } catch (e) {
      error = e?.error || e?.message || 'Could not load deleted records';
    } finally {
      loading = false;
    }
  }

  onMount(load);

  const short = (id) => (id && id.length > 12 ? id.slice(0, 8) + '…' : id);
  function goBack() { window.location.hash = '/'; }
  // Opening a tombstone id lands on the deleted-record detail (the editor's 410 state).
  function open(t) { window.location.hash = `/memory/${t.memory_id}`; }

  $: scopeLabel = ($currentTeam && $currentTeam.id && !$currentTeam.home)
    ? $currentTeam.name : 'your personal corpus';
</script>

<div class="del-page">
  <header class="del-header">
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

  <div class="del-scroll">
    <div class="del-body">
      <div class="del-eyebrow">Audit trail</div>
      <h1 class="del-title">Deleted records</h1>
      <p class="del-intro">
        Permanent tombstones for records removed from {scopeLabel}. Deletion is terminal:
        content and every revision are erased, and the address is reserved forever so it can
        never be reused. This is the record that something existed and was removed.
      </p>

      {#if loading}
        <p class="del-state">Loading…</p>
      {:else if error}
        <p class="del-state del-state--err">{error}</p>
      {:else if !deleted.length}
        <p class="del-state">Nothing has been deleted here.</p>
      {:else}
        <ul class="tombs">
          {#each deleted as t (t.memory_id)}
            <li>
              <button class="tomb" on:click={() => open(t)}>
                <span class="tomb__mark">✕</span>
                <span class="tomb__main">
                  <span class="tomb__ttl">{t.title || 'Untitled record'}</span>
                  <span class="tomb__sub">
                    {#if t.taxonomy}<code>{t.taxonomy}</code>{/if}
                    <span class="tomb__id" title={t.memory_id}>{short(t.memory_id)}</span>
                  </span>
                </span>
                <span class="tomb__when">
                  <span class="tomb__date">deleted {formatFullDate(t.deleted_at)}</span>
                  <span class="tomb__by">
                    {t.deleted_by ? `by ${short(t.deleted_by)}` : 'author unknown'}
                    · {t.final_version} version{t.final_version === 1 ? '' : 's'} destroyed
                  </span>
                </span>
              </button>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  </div>
</div>

<style>
  .del-page { height: 100%; display: flex; flex-direction: column; background: var(--bg); overflow: hidden; }
  .del-header {
    height: var(--toolbar-h); display: flex; align-items: center; gap: 10px;
    padding: 0 20px; border-bottom: 1px solid var(--border); flex-shrink: 0;
  }
  .back-btn, .refresh-btn {
    display: inline-flex; align-items: center; gap: 5px; padding: 4px 8px;
    background: transparent; border: none; border-radius: var(--radius);
    color: var(--text-2); font-size: 12px; cursor: pointer;
    transition: color var(--transition), background var(--transition);
  }
  .refresh-btn { margin-left: auto; }
  .back-btn svg, .refresh-btn svg { width: 12px; height: 12px; }
  .back-btn:hover, .refresh-btn:hover { color: var(--text); background: var(--bg-hover); }
  .refresh-btn:disabled { opacity: .5; cursor: default; }

  .del-scroll { flex: 1; overflow-y: auto; }
  .del-body { padding: 40px 48px 80px; max-width: 820px; width: 100%; margin: 0 auto; }
  @media (max-width: 700px) { .del-body { padding: 24px 18px 60px; } }

  .del-eyebrow {
    font-family: var(--font-mono); font-size: 10.5px; letter-spacing: .14em;
    text-transform: uppercase; color: var(--text-3); margin-bottom: 8px;
  }
  .del-title { font-family: var(--font-display); font-size: 2.2em; margin: 0 0 12px; color: var(--text); }
  .del-intro { font-size: 14px; color: var(--text-2); line-height: 1.6; margin: 0 0 28px; max-width: 640px; }
  .del-state { color: var(--text-3); font-size: 14px; }
  .del-state--err { color: var(--stamp); }

  .tombs { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 3px; }
  .tomb {
    display: flex; align-items: center; gap: 14px; width: 100%;
    padding: 12px 14px; background: var(--bg-subtle);
    border: 1px solid var(--border); border-radius: var(--radius-md);
    cursor: pointer; text-align: left; transition: border-color var(--transition), background var(--transition);
  }
  .tomb:hover { border-color: var(--border-mid); background: var(--bg-hover); }
  .tomb__mark {
    flex-shrink: 0; width: 24px; height: 24px; display: flex; align-items: center; justify-content: center;
    color: var(--stamp, #b4503c); background: var(--status-rejected-bg); border-radius: var(--radius); font-size: 13px;
  }
  .tomb__main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 3px; }
  .tomb__ttl { font-size: 14.5px; color: var(--text-2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .tomb__sub { display: flex; align-items: center; gap: 8px; }
  .tomb__sub code { font-family: var(--font-mono); font-size: 11px; color: var(--text-3); }
  .tomb__id { font-family: var(--font-mono); font-size: 10.5px; color: var(--text-3); }
  .tomb__when { flex-shrink: 0; display: flex; flex-direction: column; align-items: flex-end; gap: 2px; text-align: right; }
  .tomb__date { font-family: var(--font-mono); font-size: 11px; color: var(--text-2); }
  .tomb__by { font-family: var(--font-mono); font-size: 10px; color: var(--text-3); }
  @media (max-width: 560px) {
    .tomb { flex-wrap: wrap; }
    .tomb__when { align-items: flex-start; text-align: left; width: 100%; padding-left: 38px; }
  }
</style>
