<script>
  import { activeKey, identities, currentIdentity, canWrite,
           searchMode, searchQuery, taxonomyFilter, SESSION_KEY } from '../lib/store.js';
  import { apiMe, apiExportZip, apiLogout } from '../lib/api.js';
  import { loadMemories, loadCatalog } from '../lib/data.js';
  import { downloadBlob } from '../lib/utils.js';

  let open = false;
  let adding = false;
  let keyInput = '';
  let error = '';
  let busy = false;
  let addInput;

  $: name = $currentIdentity?.name || 'Unknown identity';
  $: initial = (name[0] || '?').toUpperCase();

  function toggle() {
    open = !open;
    if (!open) { adding = false; error = ''; }
  }

  async function reloadForActiveKey() {
    searchMode.set(false);
    searchQuery.set('');
    taxonomyFilter.set('');
    await Promise.all([loadMemories(), loadCatalog()]);
  }

  async function switchTo(key) {
    if (key === $activeKey) { open = false; return; }
    activeKey.set(key);
    open = false;
    await reloadForActiveKey();
  }

  function startAdd() {
    adding = true;
    error = '';
    keyInput = '';
    queueMicrotask(() => addInput?.focus());
  }

  async function addIdentity() {
    const key = keyInput.trim();
    if (!key) return;
    error = '';
    busy = true;
    try {
      const identity = await apiMe(key);
      identities.update(ids =>
        ids.find(i => i.key === key) ? ids : [...ids, { key, ...identity }]);
      adding = false;
      await switchTo(key);
    } catch (err) {
      error = err?.message || 'Invalid key';
    } finally {
      busy = false;
    }
  }

  async function removeIdentity(key, e) {
    e.stopPropagation();
    const remaining = $identities.filter(i => i.key !== key);
    identities.set(remaining);
    if (key === $activeKey) {
      if (remaining.length) await switchTo(remaining[0].key);
      else activeKey.set(null); // back to the gate
    }
  }

  let exporting = false;
  async function exportCatalog() {
    if (exporting) return;
    exporting = true;
    try {
      const { blob, filename } = await apiExportZip();
      downloadBlob(blob, filename);
      open = false;
    } catch (err) {
      error = err?.error || err?.message || 'Export failed';
    } finally {
      exporting = false;
    }
  }

  async function signOut() {
    open = false;
    // If this was a cookie session, clear it server-side too.
    if ($activeKey === SESSION_KEY) await apiLogout();
    identities.set([]);
    activeKey.set(null);
  }
</script>

<svelte:window on:keydown={e => e.key === 'Escape' && (open = false)} />

<div class="switcher" class:open>
  {#if open}
    <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
    <div class="scrim" on:click={() => (open = false)}></div>
    <div class="menu">
      <div class="menu__label">Identities</div>
      <ul class="id-list">
        {#each $identities as id (id.key)}
          <li>
            <button class="id-row" class:active={id.key === $activeKey} on:click={() => switchTo(id.key)}>
              <span class="avatar">{(id.name?.[0] || '?').toUpperCase()}</span>
              <span class="id-meta">
                <span class="id-name">{id.name || 'Unnamed'}</span>
                <span class="id-sub">{id.allow_write === false ? 'read-only' : 'read · write'}</span>
              </span>
              {#if id.key === $activeKey}
                <svg class="check" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M2.5 7.5l3 3 6-7"/></svg>
              {:else}
                <span class="remove" title="Forget this key" on:click={e => removeIdentity(id.key, e)}>×</span>
              {/if}
            </button>
          </li>
        {/each}
      </ul>

      {#if adding}
        <div class="add-form">
          <input
            bind:this={addInput}
            bind:value={keyInput}
            placeholder="mkey_…"
            autocomplete="off"
            spellcheck="false"
            on:keydown={e => { if (e.key === 'Enter') addIdentity(); }}
          />
          {#if error}<p class="add-error">{error}</p>{/if}
          <div class="add-actions">
            <button class="btn-sm" on:click={() => (adding = false)}>Cancel</button>
            <button class="btn-primary btn-add" on:click={addIdentity} disabled={busy || !keyInput.trim()}>
              {busy ? 'Checking…' : 'Add key'}
            </button>
          </div>
        </div>
      {:else}
        <button class="menu__action" on:click={startAdd}>
          <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M7 2v10M2 7h10"/></svg>
          Add identity
        </button>
        <button class="menu__action" on:click={exportCatalog} disabled={exporting}>
          <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M7 2v8M4 7l3 3 3-3M2.5 12h9"/></svg>
          {exporting ? 'Exporting…' : 'Export catalog'}
        </button>
        {#if $currentIdentity?.is_admin}
          <button class="menu__action" on:click={() => { open = false; window.location.hash = '/admin'; }}>
            <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M7 1.5l4.5 2v3.2c0 2.8-1.9 4.4-4.5 5.3-2.6-.9-4.5-2.5-4.5-5.3V3.5z"/></svg>
            Admin console
          </button>
        {/if}
        <button class="menu__action menu__action--muted" on:click={signOut}>
          <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M9 2H3v10h6M7 7h6M11 4.5L13.5 7 11 9.5"/></svg>
          Sign out
        </button>
      {/if}
    </div>
  {/if}

  <button class="current" on:click={toggle} aria-haspopup="menu" aria-expanded={open}>
    <span class="avatar">{initial}</span>
    <span class="current__meta">
      <span class="current__name">{name}</span>
      <span class="current__sub">{$canWrite ? 'read · write' : 'read-only'}</span>
    </span>
    <svg class="chev" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2.5 6.5l2.5-3 2.5 3"/></svg>
  </button>
</div>

<style>
  .switcher { position: relative; border-top: 1px solid var(--sidebar-border); padding: 8px; flex-shrink: 0; }

  .avatar {
    width: 24px; height: 24px; flex-shrink: 0;
    display: flex; align-items: center; justify-content: center;
    background: var(--accent); color: var(--accent-text);
    border-radius: var(--radius);
    font-family: var(--font-display); font-weight: 600; font-size: 12px;
  }

  .current {
    display: flex; align-items: center; gap: 9px; width: 100%;
    padding: 6px 8px; background: transparent; border: none;
    border-radius: var(--radius); cursor: pointer; text-align: left;
    transition: background var(--transition);
  }
  .current:hover { background: var(--sidebar-hover); }
  .current__meta { flex: 1; min-width: 0; display: flex; flex-direction: column; line-height: 1.25; }
  .current__name {
    font-family: var(--font-display); font-size: 14px; color: var(--sidebar-text-active);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .current__sub { font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .06em; color: var(--text-3); }
  .chev { width: 10px; height: 10px; color: var(--text-3); flex-shrink: 0; }

  .scrim { position: fixed; inset: 0; z-index: 30; }

  .menu {
    position: absolute; left: 8px; right: 8px; bottom: calc(100% - 2px);
    z-index: 31;
    background: var(--bg-raised);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-md);
    padding: 6px;
    margin-bottom: 6px;
  }
  .menu__label {
    font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .12em; text-transform: uppercase;
    color: var(--text-3); padding: 6px 8px 4px;
  }
  .id-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 1px; }

  .id-row {
    display: flex; align-items: center; gap: 9px; width: 100%;
    padding: 6px 8px; background: transparent; border: none;
    border-radius: var(--radius); cursor: pointer; text-align: left;
    transition: background var(--transition);
  }
  .id-row:hover { background: var(--bg-hover); }
  .id-row.active { background: var(--accent-wash); }
  .id-meta { flex: 1; min-width: 0; display: flex; flex-direction: column; line-height: 1.25; }
  .id-name { font-family: var(--font-display); font-size: 14px; color: var(--text);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .id-sub { font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .05em; color: var(--text-3); }
  .check { width: 14px; height: 14px; color: var(--accent); flex-shrink: 0; }
  .remove {
    width: 18px; height: 18px; flex-shrink: 0;
    display: flex; align-items: center; justify-content: center;
    color: var(--text-3); font-size: 16px; line-height: 1; border-radius: var(--radius);
  }
  .remove:hover { color: var(--stamp); background: var(--status-rejected-bg); }

  .menu__action {
    display: flex; align-items: center; gap: 8px; width: 100%;
    padding: 7px 8px; margin-top: 2px;
    background: transparent; border: none; border-radius: var(--radius);
    color: var(--text-2); font-family: var(--font); font-size: 13.5px;
    cursor: pointer; text-align: left; transition: background var(--transition), color var(--transition);
  }
  .menu__action:first-of-type { border-top: 1px solid var(--border); margin-top: 4px; padding-top: 9px; }
  .menu__action:hover { background: var(--bg-hover); color: var(--text); }
  .menu__action svg { width: 13px; height: 13px; flex-shrink: 0; }
  .menu__action--muted { color: var(--text-3); }

  .add-form { padding: 6px 4px 2px; }
  .add-form input { font-family: var(--font-mono); font-size: 12px; }
  .add-error { color: var(--stamp); font-size: 12px; margin: 6px 2px 0; }
  .add-actions { display: flex; justify-content: flex-end; gap: 6px; margin-top: 8px; }
  .btn-add { padding: 5px 12px; font-size: 12.5px; }
</style>
