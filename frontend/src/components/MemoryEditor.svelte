<script>
  import { onMount } from 'svelte';
  import { memories, catalog, canWrite, dueCount, docsMode } from '../lib/store.js';
  import { apiGetMemory, apiUpdateMemory, apiCreateMemory, apiDeleteMemory, apiCatalog, apiMemories, apiAddReview, apiRemoveReview, apiExportMemory, apiTaxonomyTemplate } from '../lib/api.js';
  import { formatFullDate, downloadBlob } from '../lib/utils.js';
  import MarkdownPreview from './MarkdownPreview.svelte';
  import RevisionHistory from './RevisionHistory.svelte';
  import { openDocsRecord } from '../lib/docs-route.js';
  import { setPageTitle } from '../lib/page-title.js';

  export let id = null;
  export let initialTaxonomy = ''; // prefill the class for a new record (from the taxonomy guide)

  // Milkdown (ProseMirror) is the heaviest dependency in the app and is only
  // needed in edit mode — load it on demand so the read/browse path stays light.
  let MilkdownEditor = null;
  let loadingEditor = false;
  async function ensureEditor() {
    if (MilkdownEditor || loadingEditor) return;
    loadingEditor = true;
    try { MilkdownEditor = (await import('./MilkdownEditor.svelte')).default; }
    finally { loadingEditor = false; }
  }

  const isNew = !id;

  // Name the tab after the record once it is loaded — on /docs this is what a
  // shared link announces itself as.
  $: if (memory?.title) setPageTitle(memory.title, { docs: $docsMode });

  let memory = null;
  let loading = !isNew;
  let error = '';
  let tombstone = null; // set when this id addresses a deleted (410) record
  let mode = isNew ? 'edit' : 'view'; // 'view' | 'edit'
  let saving = false;
  let dirty = false;
  let historyOpen = false;
  // A concurrent-write conflict surfaced on save: the record changed under us.
  // { current } holds the version now in the store; the banner offers reload or
  // force-overwrite rather than silently clobbering the other writer.
  let conflict = null;

  let editTitle = '';
  let editTaxonomy = '';
  let editFormat = 'markdown';
  let rawContent = ''; // working copy for non-markdown formats (raw text)

  const FORMATS = [
    { value: 'markdown', label: 'Markdown' },
    { value: 'mermaid',  label: 'Mermaid diagram' },
    { value: 'code',     label: 'Code' },
    { value: 'table',    label: 'Table (CSV)' },
  ];

  // Switching format swaps the editor (Milkdown ⇄ plain textarea), so carry the
  // current content across the swap.
  function onFormatChange(e) {
    const next = e.target.value;
    rawContent = editFormat === 'markdown' ? (milkdownRef?.getValue() ?? rawContent) : rawContent;
    editFormat = next;
    dirty = true;
  }

  let milkdownRef;   // bound to MilkdownEditor instance
  let titleInput;
  let metaOpen = false;

  // Titles for the [[ autocomplete in the editor (excludes this record's own).
  let allTitles = [];
  async function loadTitles() {
    try {
      const data = await apiMemories({});
      allTitles = (data.memories || [])
        .map(m => m.title)
        .filter(t => t && t !== (memory?.title || ''));
    } catch (_) { /* autocomplete is best-effort */ }
  }

  // Backlinks (other memories pointing here), grouped by relation for display.
  const REL_ORDER = ['depends-on', 'supersedes', 'contradicts', 'relates'];
  const REL_LABEL = {
    'depends-on': 'Depended on by',
    'supersedes': 'Superseded by',
    'contradicts': 'Contradicted by',
    'relates': 'Related',
  };
  $: backlinkGroups = groupBacklinks(memory?.linked_from);
  function groupBacklinks(links) {
    if (!links || !links.length) return [];
    const by = {};
    for (const l of links) (by[l.rel] ||= []).push(l);
    return REL_ORDER
      .filter(r => by[r])
      .map(r => ({ rel: r, label: REL_LABEL[r] || r, items: by[r] }));
  }
  function openLink(item) {
    if ($docsMode) openDocsRecord(item);
    else window.location.hash = `/memory/${item.id}`;
  }

  // Template branches suggested in the taxonomy picker so a writer sees the
  // corpus's intended structure even before any record exists in that branch.
  let templateBranches = [];
  async function loadTemplate() {
    try {
      const data = await apiTaxonomyTemplate();
      templateBranches = data.branches || [];
    } catch (_) { /* suggestions are best-effort */ }
  }
  // Merge template branches with the live catalog: template paths carry a
  // description (shown as the option label); catalog-only paths fill in the rest.
  $: taxonomySuggestions = mergeSuggestions($catalog, templateBranches);
  function mergeSuggestions(catalog, template) {
    const byPath = new Map();
    for (const b of template) byPath.set(b.path, { path: b.path, desc: b.description || '' });
    for (const e of catalog) if (!byPath.has(e.path)) byPath.set(e.path, { path: e.path, desc: '' });
    return [...byPath.values()];
  }

  $: canSave = editTitle.trim() && editTaxonomy.trim();

  async function load() {
    if (isNew) {
      memory = { title: '', taxonomy: initialTaxonomy || '', content: '' };
      resetForm();
      loading = false;
      return;
    }
    loading = true; error = ''; tombstone = null;
    try {
      memory = await apiGetMemory(id);
      resetForm();
    } catch (err) {
      // A deleted address comes back as 410 with the tombstone in context: show
      // the audit record instead of a generic error — the id is known, not missing.
      if (err?.code === 'deleted' && err?.context?.tombstone) {
        tombstone = err.context.tombstone;
      } else {
        error = err?.message || err?.error || 'Failed to load memory';
      }
    } finally {
      loading = false;
    }
  }

  function resetForm() {
    if (!memory) return;
    editTitle    = memory.title || '';
    editTaxonomy = memory.taxonomy || '';
    editFormat   = memory.format || 'markdown';
    rawContent   = memory.content || '';
    dirty = false;
  }

  function enterEdit() {
    ensureEditor();
    if (allTitles.length === 0) loadTitles();
    mode = 'edit';
  }

  function cancelEdit() {
    if (isNew) { goBack(); return; }
    if (dirty && !confirm('Discard unsaved changes?')) return;
    resetForm();
    mode = 'view';
  }

  // save(force): force=true drops the base_version guard, deliberately
  // overwriting a concurrent edit after the writer has seen the conflict banner.
  async function save(force = false) {
    if (isNew && !canSave) return;
    saving = true;
    try {
      const content = editFormat === 'markdown'
        ? (milkdownRef?.getValue() ?? rawContent)
        : rawContent;
      const body = {
        title:    editTitle.trim(),
        taxonomy: editTaxonomy.trim(),
        content:  content?.trim() || null,
        format:   editFormat,
      };
      if (isNew) {
        const created = await apiCreateMemory(body);
        memories.update(ms => [created, ...ms]);
        dirty = false;
        window.location.hash = `/memory/${created.id}`;
        return;
      }
      // Send the version we read, so a concurrent write is rejected rather than
      // silently clobbered — unless the writer chose to force-overwrite.
      if (!force && typeof memory?.version === 'number') body.base_version = memory.version;
      const updated = await apiUpdateMemory(id, body);
      memory = updated;
      memories.update(ms => ms.map(m => m.id === id ? updated : m));
      dirty = false;
      conflict = null;
      mode = 'view';
    } catch (err) {
      if (err?.code === 'version_conflict') {
        // Keep the writer's edits in the form; let them reload or overwrite.
        conflict = { current: err?.context?.current_version };
      } else if (err?.code === 'deleted') {
        alert('This record was deleted while you were editing; it can no longer be saved.');
        load();
      } else {
        alert(err?.message || err?.error || (isNew ? 'Could not file record' : 'Save failed'));
      }
    } finally {
      saving = false;
    }
  }

  // Discard the local edits and reload the server's current version.
  async function reloadForConflict() {
    conflict = null;
    await load();
    mode = 'view';
  }

  function onKeydown(e) {
    if ((e.metaKey || e.ctrlKey) && e.key === 's') {
      e.preventDefault();
      if (mode === 'edit') save();
    }
    if (e.key === 'Escape' && mode === 'edit') cancelEdit();
  }

  let deleting = false;
  async function remove() {
    if (!confirm(`Delete "${memory.title || 'this record'}"? This can't be undone.`)) return;
    deleting = true;
    try {
      await apiDeleteMemory(id);
      memories.update(ms => ms.filter(m => m.id !== id));
      window.location.hash = '/';
    } catch (err) {
      alert(err?.message || 'Could not delete record');
      deleting = false;
    }
  }

  let exporting = false;
  async function exportMemory() {
    if (!memory || exporting) return;
    exporting = true;
    try {
      const { blob, filename } = await apiExportMemory(id);
      downloadBlob(blob, filename);
    } catch (err) {
      alert(err?.error || err?.message || 'Export failed');
    } finally {
      exporting = false;
    }
  }

  // A restore filed a past version forward as the new head — adopt it.
  function onRestored(e) {
    const updated = e.detail;
    if (!updated) return;
    memory = updated;
    memories.update(ms => ms.map(m => m.id === id ? updated : m));
    resetForm();
    mode = 'view';
  }

  function goBack() {
    if (dirty && mode === 'edit' && !confirm('Discard unsaved changes?')) return;
    if ($docsMode) openDocsRecord(null);
    else window.location.hash = '/';
  }

  let reviewBusy = false;
  async function toggleReview() {
    if (!memory || reviewBusy) return;
    reviewBusy = true;
    const wasIn = memory.in_review;
    try {
      if (wasIn) await apiRemoveReview(id); else await apiAddReview(id);
      memory = { ...memory, in_review: !wasIn };
      dueCount.update(n => Math.max(0, wasIn ? n - 1 : n + 1));
    } catch (err) {
      alert(err?.message || err?.error || 'Could not update review deck');
    } finally {
      reviewBusy = false;
    }
  }

  onMount(() => {
    load();
    if (!$docsMode) loadTemplate();
    if (isNew) { ensureEditor(); loadTitles(); }   // new records open straight into edit mode
    if ($catalog.length === 0) {
      apiCatalog().then(d => catalog.set(d.taxonomy || [])).catch(() => {});
    }
    if (isNew) titleInput?.focus();
  });
</script>

<svelte:window on:keydown={onKeydown} />

<div class="editor-page">

  <!-- ── Header ── -->
  <header class="editor-header">
    <button class="back-btn" on:click={goBack}>
      <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M9 2L4 7l5 5"/>
      </svg>
      Catalog
    </button>

    <div class="header-center"></div>
    <div class="header-actions">
      {#if !isNew && memory && $canWrite}
        <button class="mindmap-btn" class:review-on={memory.in_review} on:click={toggleReview} disabled={reviewBusy}
                title={memory.in_review ? 'In your spaced-repetition deck — click to remove' : 'Add to spaced-repetition review'}>
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
            <rect x="2.5" y="4" width="9" height="7" rx="1.2"/><path d="M5 2.5h8.5a1 1 0 011 1V11"/>
          </svg>
          {memory.in_review ? 'In review' : 'Add to review'}
        </button>
      {/if}
      <!-- History is a read, and the public docs mirror serves it, so it stays.
           Export and Mindmap read endpoints the mirror does not. -->
      {#if !isNew && memory}
        <button class="mindmap-btn" on:click={() => (historyOpen = true)} title="View revision history and restore a past version">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
            <path d="M8 4v4l2.5 1.5M8 2.5a5.5 5.5 0 11-5.2 3.7M2.5 3v2.5H5"/>
          </svg>
          History
        </button>
      {/if}
      {#if !isNew && memory && !$docsMode}
        <button class="mindmap-btn" on:click={exportMemory} disabled={exporting} title="Download this record as a Markdown file">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
            <path d="M8 2v8M5 7l3 3 3-3M3 13h10"/>
          </svg>
          {exporting ? 'Exporting…' : 'Export .md'}
        </button>
        <button class="mindmap-btn" on:click={() => window.location.hash = `/mindmap/${id}`} title="Explore this record's link neighborhood">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
            <circle cx="8" cy="8" r="1.8"/><circle cx="3" cy="3.5" r="1.4"/><circle cx="13" cy="4" r="1.4"/><circle cx="12.5" cy="12.5" r="1.4"/>
            <path d="M6.4 6.7L4.1 4.6M9.7 6.8l2-1.7M9.2 9.3l2.3 2.3"/>
          </svg>
          Mindmap
        </button>
      {/if}
    </div>
  </header>

  <!-- ── States ── -->
  {#if loading}
    <div class="editor-center"><div class="spinner"></div></div>
  {:else if error}
    <div class="editor-center">
      <p class="error-msg">{error}</p>
      <button class="btn-ghost" on:click={load}>Retry</button>
    </div>
  {:else if tombstone}
    <div class="editor-scroll">
      <div class="editor-body">
        <div class="tomb">
          <div class="tomb__eyebrow">Deleted record</div>
          <h1 class="tomb__title">{tombstone.title || 'Untitled record'}</h1>
          {#if tombstone.taxonomy}<code class="tomb__tax">{tombstone.taxonomy}</code>{/if}
          <p class="tomb__note">
            This record was permanently deleted. Its content and every revision were
            erased and the address is reserved forever — it can't be restored or reused.
          </p>
          <dl class="tomb__facts">
            <dt>Deleted</dt><dd>{formatFullDate(tombstone.deleted_at)}</dd>
            {#if tombstone.deleted_by}<dt>By</dt><dd class="mono">{tombstone.deleted_by}</dd>{/if}
            <dt>Originally filed</dt><dd>{formatFullDate(tombstone.created_at)}</dd>
            <dt>Versions destroyed</dt><dd>{tombstone.final_version}</dd>
            <dt>Address</dt><dd class="mono">{tombstone.memory_id}</dd>
          </dl>
          <button class="btn-ghost" on:click={goBack}>Back to catalog</button>
        </div>
      </div>
    </div>
  {:else if memory}
    <div class="editor-scroll">
    <div class="editor-body">

      {#if isNew}
        <div class="new-eyebrow">New record</div>
      {/if}

      <!-- Title -->
      {#if mode === 'edit'}
        <input
          class="title-input"
          type="text"
          bind:value={editTitle}
          bind:this={titleInput}
          on:input={() => dirty = true}
          placeholder="Untitled record"
          autocomplete="off"
        />
      {:else}
        <h1 class="editor-title">{memory.title}</h1>
      {/if}

      <!-- ── Class (new) / Metadata (existing) ── -->
      {#if isNew}
        <div class="new-class-row">
          <span class="meta-label">Class <span class="required">*</span></span>
          <input
            class="meta-input meta-input--grow input-mono"
            type="text"
            bind:value={editTaxonomy}
            on:input={() => dirty = true}
            list="editor-taxonomy-list"
            placeholder="e.g. work.projects"
            autocomplete="off"
          />
          <datalist id="editor-taxonomy-list">
            {#each taxonomySuggestions as s}<option value={s.path} label={s.desc || undefined} />{/each}
          </datalist>
        </div>
      {:else}
      <details class="meta-accordion" bind:open={metaOpen}>
        <summary class="meta-summary">
          <svg class="meta-chevron" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M3 4.5l3 3 3-3"/>
          </svg>
          <span class="meta-summary__label">Metadata</span>
          {#if !metaOpen}
            <span class="meta-summary__hints">
              <code class="meta-hint-code">{memory.taxonomy || '—'}</code>
            </span>
          {/if}
        </summary>

        <div class="meta-body">
          <!-- Taxonomy -->
          <div class="meta-row">
            <span class="meta-label">Taxonomy</span>
            {#if mode === 'edit'}
              <input
                class="meta-input meta-input--grow"
                type="text"
                bind:value={editTaxonomy}
                on:input={() => dirty = true}
                list="editor-taxonomy-list"
                autocomplete="off"
              />
              <datalist id="editor-taxonomy-list">
                {#each $catalog as e}<option value={e.path} />{/each}
              </datalist>
            {:else}
              <code class="meta-value-code">{memory.taxonomy || '—'}</code>
            {/if}
          </div>

          <!-- Dates (view only) -->
          {#if mode === 'view'}
            <div class="meta-row meta-row--dates">
              <span class="meta-label">Created</span>
              <span class="meta-value-text">{formatFullDate(memory.created_at)}</span>
              <span class="meta-sep">·</span>
              <span class="meta-label">Updated</span>
              <span class="meta-value-text">{formatFullDate(memory.updated_at)}</span>
              <span class="meta-sep meta-id" title={memory.id}>{memory.id.slice(0,8)}…</span>
            </div>
          {/if}
        </div>
      </details>
      {/if}

      <hr class="editor-divider" />

      <!-- ── Content toolbar ── -->
      <div class="content-toolbar">
        {#if isNew}
          <span class="content-label">Content</span>
          <div class="save-row">
            <span class="save-hint">⌘S</span>
            <button class="btn-ghost" on:click={goBack}>Cancel</button>
            <button class="btn-primary" on:click={save} disabled={saving || !canSave}>
              {saving ? 'Filing…' : 'File record'}
            </button>
          </div>
        {:else}
          {#if $canWrite}
            <div class="mode-toggle">
              <button
                class="mode-btn"
                class:active={mode === 'view'}
                on:click={() => { if (mode === 'edit') cancelEdit(); }}
              >
                <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5">
                  <circle cx="7" cy="7" r="2.5"/>
                  <path d="M1 7c1.5-3.5 9.5-3.5 12 0-2.5 3.5-10.5 3.5-12 0z"/>
                </svg>
                View
              </button>
              <button
                class="mode-btn"
                class:active={mode === 'edit'}
                on:click={enterEdit}
              >
                <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5">
                  <path d="M9.5 2.5l2 2L4 12H2v-2L9.5 2.5z"/>
                </svg>
                Edit
              </button>
            </div>
          {:else}
            <span class="readonly-tag">read-only</span>
          {/if}

          {#if mode === 'edit'}
            <div class="save-row">
              <span class="save-hint">⌘S</span>
              <button class="btn-ghost" on:click={cancelEdit}>Cancel</button>
              <button class="btn-primary" on:click={save} disabled={saving}>
                {saving ? 'Saving…' : 'Save'}
              </button>
            </div>
          {:else if $canWrite}
            <button class="delete-btn" on:click={remove} disabled={deleting}>
              <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M2.5 4h9M5.5 4V2.5h3V4M4 4l.5 8h5L10 4"/>
              </svg>
              {deleting ? 'Deleting…' : 'Delete'}
            </button>
          {/if}
        {/if}
      </div>

      <!-- ── Format selector (edit only) ── -->
      {#if isNew || mode === 'edit'}
        <div class="format-row">
          <label class="format-row__label" for="fmt-select">Format</label>
          <select id="fmt-select" class="format-select" on:change={onFormatChange}>
            {#each FORMATS as f}
              <option value={f.value} selected={editFormat === f.value}>{f.label}</option>
            {/each}
          </select>
          {#if editFormat !== 'markdown'}
            <span class="format-row__hint">editing raw {editFormat} — preview on the right</span>
          {/if}
        </div>
      {/if}

      <!-- ── Conflict banner ── -->
      {#if conflict}
        <div class="conflict" role="alert">
          <div class="conflict__text">
            <strong>Someone else edited this record</strong>
            <span>
              It's now at version {conflict.current ?? '?'} — newer than the version you started from.
              Reload to see their changes (your edits here will be discarded), or overwrite to keep yours.
            </span>
          </div>
          <div class="conflict__actions">
            <button class="btn-ghost" on:click={reloadForConflict}>Reload theirs</button>
            <button class="btn-danger" on:click={() => save(true)} disabled={saving}>
              {saving ? 'Overwriting…' : 'Overwrite'}
            </button>
          </div>
        </div>
      {/if}

      <!-- ── Content area ── -->
      <div class="content-area">
        {#if mode === 'edit'}
          {#if editFormat === 'markdown'}
            <div class="milkdown-container">
              {#if MilkdownEditor}
                <svelte:component
                  this={MilkdownEditor}
                  bind:this={milkdownRef}
                  content={rawContent}
                  titles={allTitles}
                />
              {:else}
                <div class="editor-loading"><div class="spinner spinner--sm"></div> Loading editor…</div>
              {/if}
            </div>
          {:else}
            <div class="raw-edit">
              <textarea
                class="raw-textarea"
                bind:value={rawContent}
                on:input={() => (dirty = true)}
                spellcheck="false"
                placeholder={'Enter ' + editFormat + ' content…'}
              ></textarea>
              <div class="raw-preview">
                <MarkdownPreview content={rawContent} format={editFormat} />
              </div>
            </div>
          {/if}
        {:else}
          <MarkdownPreview content={memory.content} format={memory.format} />
        {/if}
      </div>

      <!-- ── Backlinks panel (view mode) ── -->
      {#if mode === 'view' && backlinkGroups.length}
        <section class="backlinks">
          <h2 class="backlinks__title">
            <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.4">
              <path d="M5.5 8.5l3-3M6 3.5l.7-.7a2.4 2.4 0 013.5 3.5l-.8.8M8 10.5l-.7.7a2.4 2.4 0 01-3.5-3.5l.8-.8"/>
            </svg>
            Linked from
            <span class="backlinks__count">{memory.linked_from.length}</span>
          </h2>
          {#each backlinkGroups as group}
            <div class="backlinks__group">
              <span class="backlinks__rel" data-rel={group.rel}>{group.label}</span>
              <div class="backlinks__chips">
                {#each group.items as item}
                  <button class="backlink-chip" on:click={() => openLink(item)} title={item.taxonomy}>
                    <span class="backlink-chip__title">{item.title}</span>
                    {#if item.taxonomy}<span class="backlink-chip__tax">{item.taxonomy}</span>{/if}
                  </button>
                {/each}
              </div>
            </div>
          {/each}
        </section>
      {/if}

    </div>
    </div>
  {/if}
</div>

{#if historyOpen && !isNew && id}
  <RevisionHistory
    memoryId={id}
    currentVersion={memory?.version ?? 0}
    on:restored={onRestored}
    on:close={() => (historyOpen = false)}
  />
{/if}

<style>
  .editor-page {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
    overflow: hidden;
  }

  /* ── Header ─────────────────────────────────────────────────────────────── */
  .editor-header {
    height: var(--toolbar-h);
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 0 20px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }
  .back-btn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 4px 8px;
    background: transparent;
    border: none;
    border-radius: var(--radius);
    color: var(--text-2);
    font-size: 12px;
    cursor: pointer;
    transition: color var(--transition), background var(--transition);
    flex-shrink: 0;
  }
  .back-btn svg { width: 11px; height: 11px; }
  .back-btn:hover { color: var(--text); background: var(--bg-hover); }
  .header-center { flex: 1; display: flex; align-items: center; gap: 6px; }
  .header-actions { display: flex; align-items: center; gap: 6px; }
  .mindmap-btn {
    display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px;
    background: transparent; border: 1px solid var(--border-mid); border-radius: var(--radius);
    color: var(--text-2); font-family: var(--font); font-size: 12px; cursor: pointer;
    transition: color var(--transition), border-color var(--transition), background var(--transition);
  }
  .mindmap-btn svg { width: 14px; height: 14px; }
  .mindmap-btn:hover { color: var(--text); border-color: var(--accent); background: var(--bg-hover); }
  .mindmap-btn:disabled { opacity: .55; cursor: default; }
  .mindmap-btn.review-on { color: var(--accent); border-color: var(--accent); background: var(--bg-hover); }

  /* ── Loading / error ─────────────────────────────────────────────────────── */
  .editor-center {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 12px;
    color: var(--text-2);
  }

  /* ── Scrollable body ─────────────────────────────────────────────────────── */
  .editor-scroll {
    flex: 1;
    overflow-y: auto;
  }
  .editor-body {
    padding: 40px 48px 80px;
    max-width: 780px;
    width: 100%;
    margin: 0 auto;
  }
  @media (max-width: 700px) { .editor-body { padding: 24px 18px 60px; } }
  @media (max-width: 560px) {
    .editor-title, .title-input { font-size: 1.9em; }
    .milkdown-container { padding: 16px 16px; }
    .new-class-row { flex-direction: column; align-items: stretch; gap: 6px; }
  }

  /* ── New record ──────────────────────────────────────────────────────────── */
  .new-eyebrow {
    font-family: var(--font-mono);
    font-size: 10.5px;
    font-weight: 500;
    letter-spacing: .14em;
    text-transform: uppercase;
    color: var(--text-3);
    margin-bottom: 12px;
  }
  .new-class-row {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 4px;
  }
  .content-label {
    font-family: var(--font-mono);
    font-size: 10.5px;
    font-weight: 500;
    letter-spacing: .12em;
    text-transform: uppercase;
    color: var(--text-3);
  }

  /* ── Title ───────────────────────────────────────────────────────────────── */
  .editor-title {
    font-family: var(--font-display);
    font-size: 2.5em;
    font-weight: 520;
    margin: 0 0 16px;
    line-height: 1.12;
    letter-spacing: -.02em;
    color: var(--text);
  }
  .title-input {
    display: block;
    width: 100%;
    font-family: var(--font-display);
    font-size: 2.5em;
    font-weight: 520;
    line-height: 1.12;
    letter-spacing: -.02em;
    background: transparent;
    border: none;
    border-bottom: 1px solid var(--border-mid);
    border-radius: 0;
    padding: 0 0 8px;
    margin-bottom: 16px;
    color: var(--text);
    outline: none;
    box-shadow: none;
  }
  .title-input:focus { border-bottom-color: var(--accent); box-shadow: none; }

  /* ── Metadata accordion ──────────────────────────────────────────────────── */
  .meta-accordion {
    margin-bottom: 12px;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    background: var(--bg-subtle);
    overflow: hidden;
  }
  .meta-accordion[open] { border-color: var(--border-mid); }

  .meta-summary {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 14px;
    cursor: pointer;
    list-style: none;
    user-select: none;
    transition: background var(--transition);
    min-height: 36px;
  }
  .meta-summary::-webkit-details-marker { display: none; }
  .meta-summary:hover { background: var(--bg-hover); }

  .meta-chevron {
    width: 11px;
    height: 11px;
    color: var(--text-3);
    flex-shrink: 0;
    transition: transform var(--transition);
  }
  .meta-accordion[open] .meta-chevron { transform: rotate(180deg); }

  .meta-summary__label {
    font-family: var(--font-mono);
    font-size: 10.5px;
    font-weight: 500;
    color: var(--text-3);
    text-transform: uppercase;
    letter-spacing: .12em;
    flex-shrink: 0;
  }
  .meta-summary__hints {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-left: 4px;
    overflow: hidden;
  }
  .meta-hint-code {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .meta-body {
    padding: 4px 14px 12px;
    border-top: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .meta-row {
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 28px;
  }
  .meta-row--dates { flex-wrap: wrap; gap: 6px; margin-top: 4px; }
  .meta-sep { color: var(--text-3); font-size: 11px; }
  .meta-id { margin-left: auto; font-family: var(--font-mono); font-size: 11px; color: var(--text-3); cursor: default; }

  .meta-label {
    font-family: var(--font-mono);
    font-size: 10px;
    font-weight: 500;
    letter-spacing: .1em;
    text-transform: uppercase;
    color: var(--text-3);
    white-space: nowrap;
    min-width: 76px;
  }
  .meta-value-text { font-family: var(--font-mono); font-size: 12px; color: var(--text-2); }
  .meta-value-code {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--accent);
    background: var(--bg-subtle);
    padding: 2px 8px;
    border-radius: var(--radius);
    border: 1px solid var(--border);
  }

  .meta-input {
    background: var(--bg);
    border: 1px solid transparent;
    border-radius: var(--radius);
    padding: 3px 8px;
    font-size: 13px;
    color: var(--text);
    outline: none;
    transition: border-color var(--transition);
  }
  .meta-input:focus { border-color: var(--border-focus); }
  .meta-input--grow { flex: 1; }

  .state-select {
    width: auto;
    font-size: 12px;
    padding: 3px 24px 3px 8px;
    background: var(--bg);
    border-color: var(--border);
    color: var(--text-2);
  }

  /* ── Divider ─────────────────────────────────────────────────────────────── */
  .editor-divider { border: none; border-top: 1px solid var(--border); margin: 16px 0; }

  /* ── Content toolbar ─────────────────────────────────────────────────────── */
  .content-toolbar {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    margin-bottom: 16px;
  }
  .mode-toggle {
    display: flex;
    gap: 2px;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: 2px;
  }
  .mode-btn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 4px 10px;
    font-size: 12px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--text-2);
    cursor: pointer;
    transition: all var(--transition);
  }
  .mode-btn svg { width: 12px; height: 12px; }
  .mode-btn.active { background: var(--bg-hover); color: var(--text); }
  .mode-btn:hover:not(.active) { color: var(--text); }

  .save-row { margin-left: auto; display: flex; align-items: center; gap: 8px; }
  .save-hint { font-family: var(--font-mono); font-size: 11px; color: var(--text-3); }

  .delete-btn {
    margin-left: auto;
    display: inline-flex; align-items: center; gap: 6px;
    padding: 5px 12px;
    background: transparent;
    border: 1px solid var(--border-mid);
    border-radius: var(--radius);
    color: var(--text-3);
    font-family: var(--font); font-size: 13px;
    cursor: pointer;
    transition: color var(--transition), border-color var(--transition), background var(--transition);
  }
  .delete-btn svg { width: 13px; height: 13px; }
  .delete-btn:hover:not(:disabled) { color: var(--stamp); border-color: color-mix(in srgb, var(--stamp) 35%, transparent); background: var(--status-rejected-bg); }
  .delete-btn:disabled { opacity: .5; cursor: not-allowed; }

  .readonly-tag {
    font-family: var(--font-mono); font-size: 10px; letter-spacing: .1em; text-transform: uppercase;
    color: var(--status-pending-text);
    background: var(--status-pending-bg);
    padding: 3px 9px; border-radius: 99px;
  }

  /* ── Backlinks panel ─────────────────────────────────────────────────────── */
  .backlinks {
    margin-top: 40px;
    padding-top: 22px;
    border-top: 1px solid var(--border);
  }
  .backlinks__title {
    display: flex;
    align-items: center;
    gap: 7px;
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 500;
    letter-spacing: .1em;
    text-transform: uppercase;
    color: var(--text-3);
    margin: 0 0 16px;
  }
  .backlinks__title svg { width: 13px; height: 13px; }
  .backlinks__count {
    font-size: 10px;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 1px 7px;
    color: var(--text-2);
  }
  .backlinks__group { display: flex; gap: 12px; margin-bottom: 12px; align-items: baseline; }
  @media (max-width: 560px) { .backlinks__group { flex-direction: column; gap: 6px; } }
  .backlinks__rel {
    flex-shrink: 0;
    min-width: 130px;
    font-family: var(--font-mono);
    font-size: 10.5px;
    font-weight: 600;
    letter-spacing: .03em;
    text-transform: uppercase;
    color: var(--text-2);
    padding-top: 5px;
  }
  .backlinks__rel[data-rel="depends-on"] { color: var(--accent-hover); }
  .backlinks__rel[data-rel="supersedes"],
  .backlinks__rel[data-rel="contradicts"] { color: var(--stamp, #b4503c); }
  .backlinks__chips { display: flex; flex-wrap: wrap; gap: 7px; }

  .backlink-chip {
    display: inline-flex;
    align-items: baseline;
    gap: 8px;
    padding: 5px 11px;
    background: var(--bg-subtle);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius-md);
    cursor: pointer;
    transition: border-color var(--transition), background var(--transition);
    max-width: 100%;
  }
  .backlink-chip:hover { border-color: var(--accent); background: var(--bg-hover); }
  .backlink-chip__title {
    font-size: 13.5px;
    font-weight: 540;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .backlink-chip__tax {
    font-family: var(--font-mono);
    font-size: 10.5px;
    color: var(--text-3);
    white-space: nowrap;
  }

  /* ── Content area ────────────────────────────────────────────────────────── */
  .content-area { min-height: 240px; }

  /* ── Format selector ─────────────────────────────────────────────────────── */
  .format-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 0 0 12px;
  }
  .format-row__label {
    font-family: var(--font-mono);
    font-size: 11px;
    letter-spacing: .06em;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .format-select {
    font-family: var(--font);
    font-size: 14px;
    color: var(--text);
    background: var(--bg-subtle);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius);
    padding: 5px 10px;
    cursor: pointer;
  }
  .format-select:focus { outline: none; border-color: var(--border-focus); }
  .format-row__hint { font-size: 12.5px; color: var(--text-3); font-style: italic; }

  /* ── Raw editor (non-markdown formats): textarea + live preview ───────────── */
  .raw-edit {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  @media (max-width: 720px) { .raw-edit { grid-template-columns: 1fr; } }
  .raw-textarea {
    min-height: 240px;
    resize: vertical;
    font-family: var(--font-mono);
    font-size: 13.5px;
    line-height: 1.6;
    color: var(--text);
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: 16px;
    tab-size: 2;
  }
  .raw-textarea:focus { outline: none; border-color: var(--border-focus); }
  .raw-preview {
    border: 1px dashed var(--border);
    border-radius: var(--radius-md);
    padding: 16px;
    overflow: auto;
  }

  .milkdown-container {
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: 20px 24px;
    cursor: text;
    transition: border-color var(--transition);
  }
  .milkdown-container:focus-within { border-color: var(--border-focus); }
  .editor-loading {
    display: flex; align-items: center; gap: 9px;
    min-height: 200px; color: var(--text-3); font-size: 14px;
  }
  .spinner--sm { width: 14px; height: 14px; border-width: 1.5px; }

  /* ── Conflict banner ─────────────────────────────────────────────────────── */
  .conflict {
    display: flex; align-items: center; gap: 16px; flex-wrap: wrap;
    margin: 0 0 16px; padding: 12px 14px;
    background: var(--status-pending-bg); border: 1px solid var(--status-pending-text);
    border-radius: var(--radius-md);
  }
  .conflict__text { display: flex; flex-direction: column; gap: 3px; flex: 1; min-width: 200px; }
  .conflict__text strong { font-size: 13.5px; color: var(--text); }
  .conflict__text span { font-size: 12.5px; color: var(--text-2); line-height: 1.45; }
  .conflict__actions { display: flex; gap: 8px; align-items: center; margin-left: auto; }
  .btn-danger {
    padding: 5px 14px; font-size: 12.5px; cursor: pointer;
    background: var(--stamp); color: var(--accent-text); border: 1px solid var(--stamp);
    border-radius: var(--radius); transition: opacity var(--transition);
  }
  .btn-danger:hover:not(:disabled) { opacity: .88; }
  .btn-danger:disabled { opacity: .55; cursor: default; }

  /* ── Tombstone (deleted record) ──────────────────────────────────────────── */
  .tomb { max-width: 560px; }
  .tomb__eyebrow {
    font-family: var(--font-mono); font-size: 10.5px; letter-spacing: .14em;
    text-transform: uppercase; color: var(--stamp, #b4503c); margin-bottom: 10px;
  }
  .tomb__title { font-family: var(--font-display); font-size: 2em; margin: 0 0 10px; color: var(--text-2); }
  .tomb__tax {
    font-family: var(--font-mono); font-size: 12px; color: var(--text-3);
    background: var(--bg-subtle); padding: 2px 8px; border-radius: var(--radius); border: 1px solid var(--border);
  }
  .tomb__note { font-size: 14px; color: var(--text-2); line-height: 1.6; margin: 18px 0; }
  .tomb__facts {
    display: grid; grid-template-columns: auto 1fr; gap: 6px 16px;
    margin: 0 0 24px; padding: 14px 16px;
    background: var(--bg-subtle); border: 1px solid var(--border); border-radius: var(--radius-md);
  }
  .tomb__facts dt { font-family: var(--font-mono); font-size: 10px; letter-spacing: .08em; text-transform: uppercase; color: var(--text-3); align-self: center; }
  .tomb__facts dd { margin: 0; font-size: 13px; color: var(--text-2); }
  .tomb__facts dd.mono { font-family: var(--font-mono); font-size: 11.5px; word-break: break-all; }
</style>
