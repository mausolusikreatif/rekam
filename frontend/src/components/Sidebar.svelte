<script>
  import { onMount } from 'svelte';
  import { memories, catalog, taxonomyFilter, graphView, dueCount, docsMode } from '../lib/store.js';
  import { loadMemories } from '../lib/data.js';
  import { buildTree } from '../lib/utils.js';
  import { apiReviewDue } from '../lib/api.js';
  import { openDocsRecord } from '../lib/docs-route.js';
  import IdentitySwitcher from './IdentitySwitcher.svelte';
  import TeamSwitcher from './TeamSwitcher.svelte';
  import { hasTeams } from '../lib/edition.js';
  import LinkHealthBadge from './LinkHealthBadge.svelte';
  import ThemePicker from './ThemePicker.svelte';

  // Keep the due-review badge fresh whenever the sidebar mounts. The public
  // docs corpus has no review deck and no /review endpoint, so skip it there.
  onMount(async () => {
    if ($docsMode) return;
    try {
      const data = await apiReviewDue();
      dueCount.set((data.stats && data.stats.due) || (data.cards ? data.cards.length : 0));
    } catch (_) { /* badge is best-effort */ }
  });

  // Recent: top 5 by updated_at
  $: recent = [...$memories]
    .sort((a, b) => new Date(b.updated_at) - new Date(a.updated_at))
    .slice(0, 5);

  // Taxonomy tree
  $: tree = buildTree($catalog);
  let expanded = {};   // root label → boolean

  function toggleRoot(label) {
    expanded[label] = !expanded[label];
    expanded = expanded; // trigger reactivity
  }

  function selectTaxonomy(path) {
    const isSame = $taxonomyFilter === path;
    taxonomyFilter.set(isSame ? '' : path);
    loadMemories();
  }

  function openMemory(mem) {
    if ($docsMode) openDocsRecord(mem);
    else window.location.hash = `/memory/${mem.id}`;
  }

  // Initialise all roots expanded
  $: { tree.forEach(n => { if (expanded[n.label] === undefined) expanded[n.label] = true; }); }
</script>

<aside class="sidebar">
  <div class="sidebar__header">
    <span class="logo-mark">R</span>
    <span class="sidebar__title">rekam</span>
    <span class="sidebar__tag">archive</span>
    <ThemePicker />
  </div>

  <!-- Review deck. Absent on the public docs corpus: grading is a write, and
       the read-only mirror exposes no /review endpoint. -->
  {#if !$docsMode}
  <button class="review-nav" on:click={() => window.location.hash = '/review'} title="Spaced-repetition review">
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
      <rect x="2.5" y="4" width="9" height="7" rx="1.2"/><path d="M5 2.5h8.5a1 1 0 011 1V11"/>
    </svg>
    <span class="review-nav__label">Review</span>
    {#if $dueCount > 0}<span class="review-nav__badge">{$dueCount}</span>{/if}
  </button>
  {/if}

  <!-- Recent memories -->
  <div class="sidebar__section">
    <div class="sidebar__label">Recently filed</div>
    {#if recent.length === 0}
      <p class="sidebar__empty">No memories yet</p>
    {:else}
      <ul class="recent-list">
        {#each recent as mem (mem.id)}
          <li>
            <button class="recent-item" on:click={() => openMemory(mem)}>
              <span class="recent-item__title">{mem.title}</span>
              <span class="recent-item__tax">{mem.taxonomy || '—'}</span>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="sidebar__divider"></div>

  <!-- Taxonomy tree -->
  <div class="sidebar__section sidebar__section--grow">
    <div class="sidebar__section-head">
      <div class="sidebar__label">Classes</div>
<!-- Views that need the authenticated API (templates, skills, the agent
           preview, tombstones, the mindmap). The docs mirror serves none of
           them, so they are absent there rather than dead. -->
      {#if !$docsMode}
            <button class="graph-toggle" on:click={() => window.location.hash = '/taxonomy'} title="Taxonomy template (how to classify)">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
          <path d="M3 2.5h7l3 3v8H3zM10 2.5V5.5h3M5.5 8h5M5.5 10.5h5"/>
        </svg>
      </button>
      <button class="graph-toggle" on:click={() => window.location.hash = '/skills'} title="Agent skills">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
          <path d="M8 1.5l2 4 4.3.4-3.2 2.8 1 4.3L8 10.8 3.9 13l1-4.3L1.7 5.9 6 5.5z"/>
        </svg>
      </button>
      <button class="graph-toggle" on:click={() => window.location.hash = '/agent'} title="Agent context preview">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
          <path d="M8 2l5.5 3L8 8 2.5 5 8 2z"/><path d="M2.5 8L8 11l5.5-3M2.5 11L8 14l5.5-3"/>
        </svg>
      </button>
      <button class="graph-toggle" on:click={() => window.location.hash = '/deleted'} title="Deleted records (audit trail)">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
          <path d="M3 4.5h10M6.3 4.5V3h3.4v1.5M4.5 4.5l.6 9h5.8l.6-9"/>
        </svg>
      </button>
      <button class="graph-toggle" on:click={() => window.location.hash = '/mindmap'} title="Link mindmap (how records connect)">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
          <circle cx="8" cy="8" r="1.9"/><circle cx="3" cy="3.5" r="1.5"/><circle cx="13" cy="4" r="1.5"/><circle cx="12.5" cy="12.5" r="1.5"/>
          <path d="M6.3 6.6L4.2 4.7M9.8 6.8l1.9-1.7M9.2 9.4l2.2 2.2"/>
        </svg>
      </button>
      {/if}
      <button class="graph-toggle" class:active={$graphView} on:click={() => graphView.set(!$graphView)} title="Classification map (taxonomy tree)">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="3" cy="8" r="2"/><circle cx="13" cy="3" r="2"/><circle cx="13" cy="13" r="2"/>
          <path d="M5 8h3M11 4.5l-3 2.5M11 11.5l-3-2.5"/>
        </svg>
      </button>
    </div>
    {#if tree.length === 0}
      {#if !$docsMode}
        <button class="sidebar__starter" on:click={() => window.location.hash = '/taxonomy'}>
          Start from a template →
        </button>
      {/if}
    {:else}
      <ul class="tree-list">
        {#each tree as root (root.label)}
          <!-- Root node -->
          <li class="tree-root">
            <button
              class="tree-node tree-node--root"
              class:active={$taxonomyFilter === root.path}
              on:click={() => { if (root.children.length > 0) toggleRoot(root.label); selectTaxonomy(root.path); }}
            >
              {#if root.children.length > 0}
                <svg class="tree-chevron" class:open={expanded[root.label]} viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.5">
                  <path d="M2.5 3.5l2.5 3 2.5-3"/>
                </svg>
              {:else}
                <span class="tree-dot"></span>
              {/if}
              <span class="tree-node__label">{root.label}</span>
              <span class="tree-count">{root.count}</span>
            </button>

            <!-- Children -->
            {#if root.children.length > 0 && expanded[root.label]}
              <ul class="tree-children">
                {#each root.children as child (child.path)}
                  <li>
                    <button
                      class="tree-node tree-node--child"
                      class:active={$taxonomyFilter === child.path}
                      on:click={() => selectTaxonomy(child.path)}
                    >
                      <span class="tree-node__label">{child.label}</span>
                      <span class="tree-count">{child.count}</span>
                    </button>
                  </li>
                {/each}
              </ul>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  {#if !$docsMode}
    <LinkHealthBadge />
    {#if hasTeams}<TeamSwitcher />{/if}
    <IdentitySwitcher />
  {/if}
</aside>

<style>
  .sidebar {
    width: var(--sidebar-w);
    background: var(--sidebar-bg);
    border-right: 1px solid var(--sidebar-border);
    display: flex;
    flex-direction: column;
    flex-shrink: 0;
    overflow: hidden;
  }

  .sidebar__header {
    display: flex;
    align-items: center;
    gap: 9px;
    height: var(--toolbar-h);
    padding: 0 18px;
    border-bottom: 1px solid var(--sidebar-border);
    flex-shrink: 0;
  }
  .logo-mark {
    width: 26px; height: 26px;
    background: var(--accent); color: var(--accent-text);
    border-radius: var(--radius);
    box-shadow: inset 0 0 0 1px rgba(255,255,255,.12), var(--shadow-sm);
    display: flex; align-items: center; justify-content: center;
    font-family: var(--font-display);
    font-weight: 600; font-size: 15px; flex-shrink: 0;
  }
  .sidebar__title {
    font-family: var(--font-display);
    font-size: 20px; font-weight: 560; letter-spacing: -.01em;
    color: var(--sidebar-text-active);
  }
  .sidebar__tag {
    margin-left: auto;
    font-family: var(--font-mono);
    font-size: 9px; letter-spacing: .16em; text-transform: uppercase;
    color: var(--text-3);
    border: 1px solid var(--border-mid);
    border-radius: 99px;
    padding: 2px 7px;
  }

  .sidebar__divider { height: 1px; background: var(--sidebar-border); flex-shrink: 0; }

  .review-nav {
    display: flex; align-items: center; gap: 8px; margin: 10px 12px 2px; padding: 8px 12px;
    background: transparent; border: 1px solid var(--border-mid); border-radius: var(--radius-md);
    color: var(--sidebar-text-active); font-family: var(--font); font-size: 13px; cursor: pointer;
    transition: background var(--transition), border-color var(--transition);
  }
  .review-nav:hover { background: var(--sidebar-hover); border-color: var(--accent); }
  .review-nav svg { width: 15px; height: 15px; color: var(--accent); flex-shrink: 0; }
  .review-nav__label { flex: 1; text-align: left; font-weight: 500; }
  .review-nav__badge {
    font-family: var(--font-mono); font-size: 11px; font-weight: 600; color: var(--accent-text);
    background: var(--accent); border-radius: 99px; padding: 1px 8px; font-variant-numeric: tabular-nums;
  }

  .sidebar__section {
    padding: 10px 0 6px;
    flex-shrink: 0;
  }
  .sidebar__section--grow { flex: 1; overflow-y: auto; }

  .sidebar__section-head {
    display: flex;
    align-items: center;
    padding: 0 8px 5px 14px;
  }
  .sidebar__section-head .sidebar__label { padding: 0; flex: 1; }

  .graph-toggle {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 22px; height: 22px;
    padding: 0;
    background: transparent;
    border: 1px solid transparent;
    border-radius: var(--radius);
    color: var(--sidebar-text);
    cursor: pointer;
    transition: background var(--transition), color var(--transition), border-color var(--transition);
    flex-shrink: 0;
  }
  .graph-toggle:hover { background: var(--sidebar-hover); color: var(--sidebar-text-active); }
  .graph-toggle.active { background: var(--sidebar-active); color: var(--accent); border-color: var(--sidebar-active-border); }
  .graph-toggle svg { width: 13px; height: 13px; }

  .sidebar__label {
    font-family: var(--font-mono);
    font-size: 10px; font-weight: 500;
    color: var(--text-3);
    letter-spacing: .13em; text-transform: uppercase;
    padding: 0 16px 6px;
  }
  .sidebar__empty { font-size: 13px; color: var(--sidebar-text); padding: 0 16px; margin: 0; font-style: italic; }
  .sidebar__starter {
    display: block; width: calc(100% - 20px); margin: 0 10px; padding: 8px 10px;
    background: transparent; border: 1px dashed var(--sidebar-border); border-radius: var(--radius);
    color: var(--sidebar-text); font-family: var(--font); font-size: 12.5px; text-align: left;
    cursor: pointer; transition: color var(--transition), border-color var(--transition);
  }
  .sidebar__starter:hover { color: var(--sidebar-text-active); border-color: var(--accent); }

  /* ── Recent memories ──────────────────────────────────────────────────────── */
  .recent-list { list-style: none; margin: 0; padding: 0 6px; display: flex; flex-direction: column; gap: 1px; }

  .recent-item {
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: 1px;
    padding: 5px 8px;
    background: transparent;
    border: none;
    border-radius: var(--radius);
    text-align: left;
    cursor: pointer;
    transition: background var(--transition);
  }
  .recent-item:hover { background: var(--sidebar-hover); }

  .recent-item__title {
    font-family: var(--font-display);
    font-size: 14px;
    font-weight: 460;
    color: var(--sidebar-text-active);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    display: block;
  }
  .recent-item__tax {
    font-size: 10.5px;
    font-family: var(--font-mono);
    color: var(--accent);
    opacity: .85;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    display: block;
  }

  /* ── Taxonomy tree ─────────────────────────────────────────────────────────── */
  .tree-list { list-style: none; margin: 0; padding: 0 6px; display: flex; flex-direction: column; gap: 1px; }
  .tree-root { display: flex; flex-direction: column; }

  .tree-node {
    display: flex;
    align-items: center;
    gap: 5px;
    width: 100%;
    padding: 5px 8px;
    background: transparent;
    border: none;
    border-radius: var(--radius);
    cursor: pointer;
    text-align: left;
    transition: background var(--transition);
    color: var(--sidebar-text);
  }
  .tree-node:hover { background: var(--sidebar-hover); color: var(--sidebar-text-active); }
  .tree-node.active {
    background: var(--sidebar-active);
    border-left: 2px solid var(--sidebar-active-border);
    color: var(--sidebar-text-active);
    padding-left: 6px;
  }

  .tree-chevron {
    width: 10px; height: 10px; flex-shrink: 0;
    transition: transform var(--transition);
    color: var(--sidebar-text);
  }
  .tree-chevron.open { transform: rotate(180deg); }

  .tree-dot {
    width: 4px; height: 4px; flex-shrink: 0;
    border-radius: 50%;
    background: var(--sidebar-text);
    margin: 0 3px;
  }

  .tree-node__label {
    flex: 1;
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 400;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .tree-node--root .tree-node__label { font-weight: 500; }
  .tree-node--child .tree-node__label { font-weight: 400; font-size: 11.5px; opacity: .92; }

  .tree-count {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-3);
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    padding: 0 6px;
    border-radius: 99px;
    flex-shrink: 0;
    font-variant-numeric: tabular-nums;
  }
  .tree-node.active .tree-count { background: var(--accent); border-color: var(--accent); color: var(--accent-text); }

  .tree-children {
    list-style: none;
    margin: 1px 0 0;
    padding: 0 0 0 18px;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
</style>
