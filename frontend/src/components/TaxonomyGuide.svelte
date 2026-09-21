<script>
  import { onMount } from 'svelte';
  import { currentTeam } from '../lib/store.js';
  import { apiTaxonomyTemplate, apiSetTaxonomyTemplate } from '../lib/api.js';

  let branches = [];
  let source = 'default';   // 'default' | 'custom'
  let kind = 'personal';    // 'personal' | 'team'
  let canEdit = false;
  let loading = true;
  let error = '';

  let editing = false;
  let draft = [];           // working copy while editing
  let saving = false;

  async function load() {
    loading = true;
    error = '';
    try {
      const data = await apiTaxonomyTemplate();
      branches = data.branches || [];
      source = data.source || 'default';
      kind = data.kind || 'personal';
      canEdit = !!data.can_edit;
    } catch (e) {
      error = e?.error || e?.message || 'Could not load the taxonomy template';
    } finally {
      loading = false;
    }
  }

  onMount(load);

  // Depth by dot count, so children indent under their parent.
  const depthOf = (path) => (path.match(/\./g) || []).length;
  const leaf = (path) => path.slice(path.lastIndexOf('.') + 1);

  function goBack() { window.location.hash = '/'; }
  function newHere(path) { window.location.hash = `/new/${encodeURIComponent(path)}`; }

  function startEdit() {
    draft = branches.map((b) => ({ path: b.path, description: b.description || '' }));
    if (draft.length === 0) draft = [{ path: '', description: '' }];
    editing = true;
    error = '';
  }
  function cancelEdit() { editing = false; error = ''; }
  function addRow() { draft = [...draft, { path: '', description: '' }]; }
  function removeRow(i) { draft = draft.filter((_, idx) => idx !== i); }

  async function save() {
    // Drop empty rows; a branch needs a path to be addressable.
    const clean = draft
      .map((b) => ({ path: b.path.trim(), description: b.description.trim() }))
      .filter((b) => b.path);
    saving = true;
    error = '';
    try {
      const data = await apiSetTaxonomyTemplate(clean);
      branches = data.branches || [];
      source = data.source || 'custom';
      canEdit = !!data.can_edit;
      editing = false;
    } catch (e) {
      error = e?.message || e?.error || 'Could not save the template';
    } finally {
      saving = false;
    }
  }

  async function resetToDefault() {
    if (!confirm('Reset to the shipped default template? Your custom branches will be discarded.')) return;
    saving = true;
    error = '';
    try {
      const data = await apiSetTaxonomyTemplate([]); // empty reverts to default
      branches = data.branches || [];
      source = data.source || 'default';
      editing = false;
    } catch (e) {
      error = e?.message || e?.error || 'Could not reset the template';
    } finally {
      saving = false;
    }
  }

  $: scopeLabel = ($currentTeam && $currentTeam.id && !$currentTeam.home)
    ? $currentTeam.name : 'your personal corpus';
</script>

<div class="tax-page">
  <header class="tax-header">
    <button class="back-btn" on:click={goBack}>
      <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9 2L4 7l5 5"/></svg>
      Catalog
    </button>
    {#if canEdit && !editing && !loading}
      <button class="edit-btn" on:click={startEdit}>
        <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9.5 2.5l2 2L4 12H2v-2L9.5 2.5z"/></svg>
        Edit template
      </button>
    {/if}
  </header>

  <div class="tax-scroll">
    <div class="tax-body">
      <div class="tax-eyebrow">Onboarding · {kind}</div>
      <h1 class="tax-title">Taxonomy template</h1>
      <p class="tax-intro">
        How records are classified in {scopeLabel}. These are the branches to file into —
        for you and for any AI agent writing here. File into a branch that fits rather than
        inventing a parallel one; the tree grows only when nothing fits.
        {#if source === 'default'}
          <span class="tax-badge">shipped default</span>
        {:else}
          <span class="tax-badge tax-badge--custom">customized</span>
        {/if}
      </p>

      {#if error}<p class="tax-state tax-state--err">{error}</p>{/if}

      {#if loading}
        <p class="tax-state">Loading…</p>

      {:else if editing}
        <!-- ── Editor ── -->
        <div class="edit-list">
          {#each draft as row, i (i)}
            <div class="edit-row">
              <input class="edit-path" bind:value={row.path} placeholder="branch.path" autocomplete="off" spellcheck="false" />
              <input class="edit-desc" bind:value={row.description} placeholder="what goes here" autocomplete="off" />
              <button class="row-remove" title="Remove branch" on:click={() => removeRow(i)}>×</button>
            </div>
          {/each}
        </div>
        <button class="add-row" on:click={addRow}>
          <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M7 2v10M2 7h10"/></svg>
          Add branch
        </button>
        <div class="edit-actions">
          {#if source === 'custom'}
            <button class="btn-ghost reset-btn" on:click={resetToDefault} disabled={saving}>Reset to default</button>
          {/if}
          <span class="edit-actions__spacer"></span>
          <button class="btn-ghost" on:click={cancelEdit} disabled={saving}>Cancel</button>
          <button class="btn-primary" on:click={save} disabled={saving}>{saving ? 'Saving…' : 'Save template'}</button>
        </div>
        <p class="edit-hint">Use dot-notation for nesting (e.g. <code>work.projects</code>). An empty template reverts to the shipped default.</p>

      {:else if !branches.length}
        <p class="tax-state">No template defined.</p>

      {:else}
        <!-- ── Read view ── -->
        <ul class="branch-list">
          {#each branches as b (b.path)}
            <li class="branch" style="--depth: {depthOf(b.path)}">
              <div class="branch__main">
                <code class="branch__path">
                  {#if depthOf(b.path) > 0}<span class="branch__parent">{b.path.slice(0, b.path.lastIndexOf('.') + 1)}</span>{/if}<span class="branch__leaf">{leaf(b.path)}</span>
                </code>
                {#if b.description}<span class="branch__desc">{b.description}</span>{/if}
              </div>
              <button class="branch__new" on:click={() => newHere(b.path)} title="New record in this branch">
                <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M7 2v10M2 7h10"/></svg>
                New here
              </button>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  </div>
</div>

<style>
  .tax-page { height: 100%; display: flex; flex-direction: column; background: var(--bg); overflow: hidden; }
  .tax-header {
    height: var(--toolbar-h); display: flex; align-items: center; gap: 10px;
    padding: 0 20px; border-bottom: 1px solid var(--border); flex-shrink: 0;
  }
  .back-btn, .edit-btn {
    display: inline-flex; align-items: center; gap: 5px; padding: 4px 8px;
    background: transparent; border: none; border-radius: var(--radius);
    color: var(--text-2); font-size: 12px; cursor: pointer;
    transition: color var(--transition), background var(--transition);
  }
  .edit-btn { margin-left: auto; border: 1px solid var(--border-mid); }
  .back-btn svg, .edit-btn svg { width: 12px; height: 12px; }
  .back-btn:hover, .edit-btn:hover { color: var(--text); background: var(--bg-hover); }

  .tax-scroll { flex: 1; overflow-y: auto; }
  .tax-body { padding: 40px 48px 80px; max-width: 780px; width: 100%; margin: 0 auto; }
  @media (max-width: 700px) { .tax-body { padding: 24px 18px 60px; } }

  .tax-eyebrow {
    font-family: var(--font-mono); font-size: 10.5px; letter-spacing: .14em;
    text-transform: uppercase; color: var(--text-3); margin-bottom: 8px;
  }
  .tax-title { font-family: var(--font-display); font-size: 2.2em; margin: 0 0 12px; color: var(--text); }
  .tax-intro { font-size: 14px; color: var(--text-2); line-height: 1.6; margin: 0 0 28px; max-width: 640px; }
  .tax-badge {
    display: inline-block; margin-left: 6px; font-family: var(--font-mono); font-size: 9.5px;
    letter-spacing: .08em; text-transform: uppercase; padding: 2px 7px; border-radius: 99px;
    color: var(--text-3); background: var(--bg-subtle); border: 1px solid var(--border);
  }
  .tax-badge--custom { color: var(--accent); border-color: var(--accent); }
  .tax-state { color: var(--text-3); font-size: 14px; }
  .tax-state--err { color: var(--stamp); }

  /* ── Read view ── */
  .branch-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 1px; }
  .branch {
    display: flex; align-items: center; gap: 14px;
    padding: 10px 12px 10px calc(12px + var(--depth) * 20px);
    border-radius: var(--radius-md); border: 1px solid transparent;
    transition: background var(--transition), border-color var(--transition);
  }
  .branch:hover { background: var(--bg-subtle); border-color: var(--border); }
  .branch__main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 3px; }
  .branch__path { font-family: var(--font-mono); font-size: 13px; }
  .branch__parent { color: var(--text-3); }
  .branch__leaf { color: var(--accent); font-weight: 600; }
  .branch__desc { font-size: 13px; color: var(--text-2); line-height: 1.4; }
  .branch__new {
    flex-shrink: 0; display: inline-flex; align-items: center; gap: 5px; padding: 4px 10px;
    background: transparent; border: 1px solid var(--border-mid); border-radius: var(--radius);
    color: var(--text-3); font-family: var(--font); font-size: 12px; cursor: pointer;
    opacity: 0; transition: opacity var(--transition), color var(--transition), border-color var(--transition);
  }
  .branch:hover .branch__new { opacity: 1; }
  .branch__new:hover { color: var(--accent); border-color: var(--accent); }
  .branch__new svg { width: 11px; height: 11px; }
  @media (max-width: 560px) { .branch__new { opacity: 1; } }

  /* ── Editor ── */
  .edit-list { display: flex; flex-direction: column; gap: 6px; margin-bottom: 10px; }
  .edit-row { display: flex; gap: 8px; align-items: center; }
  .edit-path {
    flex: 0 0 200px; font-family: var(--font-mono); font-size: 12.5px;
    padding: 6px 9px; border: 1px solid var(--border-mid); border-radius: var(--radius);
    background: var(--bg-subtle); color: var(--text);
  }
  .edit-desc {
    flex: 1; font-size: 13px; padding: 6px 9px;
    border: 1px solid var(--border-mid); border-radius: var(--radius);
    background: var(--bg-subtle); color: var(--text);
  }
  .edit-path:focus, .edit-desc:focus { outline: none; border-color: var(--border-focus); }
  .row-remove {
    flex-shrink: 0; width: 26px; height: 26px; display: flex; align-items: center; justify-content: center;
    color: var(--text-3); font-size: 18px; line-height: 1; border: none; background: none;
    border-radius: var(--radius); cursor: pointer;
  }
  .row-remove:hover { color: var(--stamp); background: var(--status-rejected-bg); }
  @media (max-width: 560px) {
    .edit-row { flex-wrap: wrap; }
    .edit-path { flex-basis: 100%; }
  }

  .add-row {
    display: inline-flex; align-items: center; gap: 6px; padding: 6px 10px; margin-top: 2px;
    background: transparent; border: 1px dashed var(--border-mid); border-radius: var(--radius);
    color: var(--text-2); font-size: 12.5px; cursor: pointer; transition: color var(--transition), border-color var(--transition);
  }
  .add-row:hover { color: var(--text); border-color: var(--accent); }
  .add-row svg { width: 12px; height: 12px; }

  .edit-actions { display: flex; align-items: center; gap: 8px; margin-top: 20px; }
  .edit-actions__spacer { flex: 1; }
  .reset-btn { color: var(--stamp); }
  .edit-hint { font-size: 12px; color: var(--text-3); margin-top: 10px; }
  .edit-hint code { font-family: var(--font-mono); background: var(--bg-subtle); padding: 1px 5px; border-radius: 4px; }
</style>
