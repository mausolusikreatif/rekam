<script>
  import { canWrite } from '../lib/store.js';

  // Mirror of the MCP tool registry in internal/mcp/server.go. The server is the
  // source of truth; this is the human-readable capability sheet for the same set.
  const READ = [
    {
      name: 'catalog', verb: 'Survey',
      blurb: 'Return every taxonomy path with its record count. The agent calls this first to learn how the archive is classified before querying anything.',
      params: [],
    },
    {
      name: 'memory_scope', verb: 'Browse',
      blurb: 'List the records under a class — titles only, no bodies — newest first, trimmed to a token budget. The cheapest way to see what a class holds.',
      params: [
        { k: 'taxonomy', req: true, d: 'class prefix, e.g. work.projects' },
        { k: 'token_budget', d: 'max tokens (default 2000)' },
      ],
    },
    {
      name: 'search', verb: 'Search',
      blurb: 'Full-text search across record titles and bodies, scoped to a class. Returns ranked snippets and an omitted_count so the agent knows when to narrow.',
      params: [
        { k: 'q', req: true, d: 'FTS5 query' },
        { k: 'taxonomy', d: 'class prefix to scope' },
        { k: 'token_budget', d: 'max tokens to return' },
      ],
    },
    {
      name: 'get_memory', verb: 'Read',
      blurb: 'Fetch one record in full by ID, including its complete markdown body. Used once a snippet proves insufficient.',
      params: [{ k: 'id', req: true, d: 'record UUID' }],
    },
  ];

  const WRITE = [
    {
      name: 'write_memory', verb: 'File',
      blurb: 'Create a new record. The agent classifies it into a dot-notation taxonomy, writes a scannable title, and stores a markdown body.',
      params: [
        { k: 'title', req: true, d: 'concise, scannable' },
        { k: 'taxonomy', req: true, d: 'dot-notation path' },
        { k: 'content', d: 'markdown body' },
      ],
    },
    {
      name: 'update_memory', verb: 'Amend',
      blurb: 'Patch fields on an existing record — only the fields passed change. Used to correct, reclassify, or extend a record over time.',
      params: [
        { k: 'id', req: true, d: 'record UUID' },
        { k: 'title', d: 'new title' },
        { k: 'content', d: 'new markdown body' },
        { k: 'taxonomy', d: 'new class' },
      ],
    },
    {
      name: 'delete_memory', verb: 'Withdraw',
      blurb: 'Permanently remove a record by ID. There is no undo — the record leaves the catalog for good.',
      params: [{ k: 'id', req: true, d: 'record UUID' }],
    },
    {
      name: 'rebuild_fts', verb: 'Reindex',
      blurb: 'Rebuild the full-text search index from scratch. A maintenance operation, run when search results drift out of sync with the records.',
      params: [],
    },
  ];

  function goBack() { window.location.hash = '/'; }
</script>

<div class="skills-page">
  <header class="skills-header">
    <button class="back-btn" on:click={goBack}>
      <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9 2L4 7l5 5"/></svg>
      Catalog
    </button>
  </header>

  <div class="skills-scroll">
    <div class="skills-body">
      <div class="skills-eyebrow">Agent capabilities</div>
      <h1 class="skills-title">What an agent can do</h1>
      <p class="skills-intro">
        Connected over MCP, an agent works the catalog through eight tools. Reading is always
        available; writing is granted per identity. This is the same toolset the model is handed —
        the manifest below is its job description.
      </p>

      <!-- Protocol -->
      <section class="protocol">
        <div class="protocol__col">
          <div class="protocol__head">Retrieval — in order</div>
          <ol class="protocol__list">
            <li><code>catalog</code> to learn the classes</li>
            <li>pick relevant paths — never query everything</li>
            <li><code>memory_scope</code> to browse titles when a class is small</li>
            <li><code>search</code> with a query <em>and</em> a class prefix</li>
            <li>if <code>omitted_count &gt; 0</code>, narrow and retry</li>
            <li><code>get_memory</code> when a snippet isn't enough</li>
          </ol>
        </div>
        <div class="protocol__col">
          <div class="protocol__head">Writing — in order</div>
          <ol class="protocol__list">
            <li>classify into a dot-notation taxonomy</li>
            <li>write a concise, scannable title</li>
            <li>compose a markdown body with context for a future reader</li>
            <li><code>write_memory</code> to file it</li>
            <li><code>update_memory</code> to amend, <code>delete_memory</code> to withdraw</li>
          </ol>
        </div>
      </section>

      <!-- Read tools -->
      <div class="group-head">
        <span class="group-label">Read</span>
        <span class="group-meta">always available · {READ.length} tools</span>
      </div>
      <ul class="tools">
        {#each READ as t (t.name)}
          <li class="tool">
            <div class="tool__top">
              <span class="tool__verb">{t.verb}</span>
              <code class="tool__name">{t.name}</code>
              <span class="tool__kind tool__kind--read">read-only</span>
            </div>
            <p class="tool__blurb">{t.blurb}</p>
            {#if t.params.length}
              <div class="tool__params">
                {#each t.params as p (p.k)}
                  <span class="param" class:param--req={p.req}>
                    <code>{p.k}</code>{#if p.req}<span class="param__star">*</span>{/if}
                    <span class="param__d">{p.d}</span>
                  </span>
                {/each}
              </div>
            {:else}
              <div class="tool__params"><span class="param param--none">no arguments</span></div>
            {/if}
          </li>
        {/each}
      </ul>

      <!-- Write tools -->
      <div class="group-head">
        <span class="group-label">Write</span>
        <span class="group-meta">
          {#if $canWrite}granted to this identity{:else}withheld — this key is read-only{/if} · {WRITE.length} tools
        </span>
      </div>
      <ul class="tools" class:tools--locked={!$canWrite}>
        {#each WRITE as t (t.name)}
          <li class="tool tool--write">
            <div class="tool__top">
              <span class="tool__verb">{t.verb}</span>
              <code class="tool__name">{t.name}</code>
              <span class="tool__kind tool__kind--write">writes</span>
            </div>
            <p class="tool__blurb">{t.blurb}</p>
            {#if t.params.length}
              <div class="tool__params">
                {#each t.params as p (p.k)}
                  <span class="param" class:param--req={p.req}>
                    <code>{p.k}</code>{#if p.req}<span class="param__star">*</span>{/if}
                    <span class="param__d">{p.d}</span>
                  </span>
                {/each}
              </div>
            {:else}
              <div class="tool__params"><span class="param param--none">no arguments</span></div>
            {/if}
          </li>
        {/each}
      </ul>

      {#if !$canWrite}
        <p class="locked-note">
          The active identity holds a read-only key, so the write tools above are not exposed to its
          agents. Switch to a read·write identity to grant them.
        </p>
      {/if}
    </div>
  </div>
</div>

<style>
  .skills-page { height: 100%; display: flex; flex-direction: column; background: var(--bg); overflow: hidden; }

  .skills-header {
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

  .skills-scroll { flex: 1; overflow-y: auto; }
  .skills-body { padding: 40px 48px 80px; max-width: 860px; width: 100%; margin: 0 auto; }
  @media (max-width: 700px) { .skills-body { padding: 24px 18px 60px; } }

  .skills-eyebrow {
    font-family: var(--font-mono); font-size: 10.5px; font-weight: 500;
    letter-spacing: .14em; text-transform: uppercase; color: var(--text-3); margin-bottom: 12px;
  }
  .skills-title {
    font-family: var(--font-display); font-size: 2.3em; font-weight: 520;
    line-height: 1.12; letter-spacing: -.02em; margin: 0 0 14px; color: var(--text);
  }
  @media (max-width: 560px) { .skills-title { font-size: 1.9em; } }
  .skills-intro { font-size: 15.5px; line-height: 1.6; color: var(--text-2); max-width: 64ch; margin: 0 0 30px; }

  /* Protocol */
  .protocol {
    display: grid; grid-template-columns: 1fr 1fr; gap: 28px;
    padding: 22px 24px; margin-bottom: 36px;
    background: var(--bg-subtle); border: 1px solid var(--border); border-radius: var(--radius-md);
  }
  @media (max-width: 640px) { .protocol { grid-template-columns: 1fr; gap: 22px; padding: 18px; } }
  .protocol__head {
    font-family: var(--font-mono); font-size: 10px; font-weight: 500;
    letter-spacing: .12em; text-transform: uppercase; color: var(--text-3); margin-bottom: 10px;
  }
  .protocol__list { margin: 0; padding-left: 18px; display: flex; flex-direction: column; gap: 6px; }
  .protocol__list li { font-size: 13.5px; line-height: 1.45; color: var(--text-2); }
  .protocol__list code {
    font-family: var(--font-mono); font-size: 12px; color: var(--accent);
    background: var(--bg-raised); border: 1px solid var(--border); border-radius: 3px; padding: 0 4px;
  }
  .protocol__list em { font-style: italic; color: var(--text); }

  /* Group headers */
  .group-head { display: flex; align-items: baseline; gap: 10px; margin: 0 0 14px; padding-bottom: 8px; border-bottom: 1px solid var(--border-mid); }
  .group-label { font-family: var(--font-display); font-size: 20px; font-weight: 540; color: var(--text); }
  .group-meta { font-family: var(--font-mono); font-size: 11px; color: var(--text-3); }

  /* Tools */
  .tools { list-style: none; margin: 0 0 34px; padding: 0; display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
  @media (max-width: 640px) { .tools { grid-template-columns: 1fr; } }
  .tools--locked { opacity: .58; }

  .tool {
    padding: 15px 17px; border: 1px solid var(--border); border-radius: var(--radius-md);
    background: var(--bg-raised); border-left: 2px solid var(--accent);
    display: flex; flex-direction: column; gap: 9px;
  }
  .tool--write { border-left-color: var(--stamp); }

  .tool__top { display: flex; align-items: center; gap: 9px; flex-wrap: wrap; }
  .tool__verb { font-family: var(--font-display); font-size: 16px; font-weight: 520; color: var(--text); }
  .tool__name {
    font-family: var(--font-mono); font-size: 12px; color: var(--accent);
    background: var(--accent-wash); border-radius: 3px; padding: 1px 7px;
  }
  .tool--write .tool__name { color: var(--stamp); background: var(--status-rejected-bg); }
  .tool__kind {
    margin-left: auto; font-family: var(--font-mono); font-size: 9px; letter-spacing: .1em;
    text-transform: uppercase; padding: 2px 7px; border-radius: 99px;
  }
  .tool__kind--read { color: var(--state-resolved-text); background: var(--state-resolved-bg); }
  .tool__kind--write { color: var(--stamp); background: var(--status-rejected-bg); }

  .tool__blurb { margin: 0; font-size: 13.5px; line-height: 1.5; color: var(--text-2); }

  .tool__params { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 1px; }
  .param {
    display: inline-flex; align-items: baseline; gap: 5px;
    padding: 3px 8px; border-radius: var(--radius);
    background: var(--bg-subtle); border: 1px solid var(--border);
  }
  .param code { font-family: var(--font-mono); font-size: 11px; color: var(--text); }
  .param--req code { color: var(--text); font-weight: 500; }
  .param__star { color: var(--stamp); font-family: var(--font-mono); font-size: 11px; margin-left: -3px; }
  .param__d { font-size: 11px; color: var(--text-3); }
  .param--none { font-family: var(--font-mono); font-size: 11px; color: var(--text-3); font-style: normal; }

  .locked-note {
    font-size: 13.5px; line-height: 1.55; color: var(--text-2);
    background: var(--status-pending-bg); border: 1px solid color-mix(in srgb, var(--status-pending-text) 30%, var(--status-pending-bg)); border-radius: var(--radius-md);
    padding: 14px 16px; margin: -14px 0 0;
  }
</style>
