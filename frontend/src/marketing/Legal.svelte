<script>
  // The legal text is rendered to HTML at build time (see the markdown plugin
  // in vite.marketing.config.js) rather than with the app's MarkdownPreview,
  // which drags in katex, mermaid and DOMPurify for two pages of prose that
  // cannot change between builds.
  //
  // The canonical source stays docs/legal/*.md in the repo root, so there is
  // still exactly one copy of the text — the same import the app used.
  export let html = '';
</script>

<div class="legal">
  <div class="legal__inner">
    <a class="legal__back" href="/">&larr; Back to rekam</a>
    <div class="legal__doc">
      {@html html}
    </div>
  </div>
</div>

<style>
  .legal {
    min-height: 100vh;
    background: var(--bg);
    padding: 40px 20px 80px;
  }
  .legal__inner {
    max-width: 760px;
    margin: 0 auto;
  }
  .legal__back {
    display: inline-block;
    margin-bottom: 20px;
    font-size: 13px;
    color: var(--text-2);
    text-decoration: none;
  }
  .legal__back:hover { text-decoration: underline; }
  .legal__doc {
    background: var(--bg-raised);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius-md);
    padding: 32px 40px;
  }

  /* Prose for the injected HTML. Svelte's scoping does not reach {@html}
     content, so these are :global() under .legal__doc — deliberately narrow,
     and on the same tokens as the rest of the site rather than a second
     typographic system for two pages. */
  .legal__doc :global(h1) {
    font-family: var(--font-display, Georgia, serif);
    font-size: 28px;
    line-height: 1.2;
    margin: 0 0 20px;
    color: var(--text);
  }
  .legal__doc :global(h2) {
    font-family: var(--font-display, Georgia, serif);
    font-size: 19px;
    line-height: 1.3;
    margin: 32px 0 10px;
    color: var(--text);
  }
  .legal__doc :global(h3) {
    font-size: 15px;
    margin: 24px 0 8px;
    color: var(--text);
  }
  .legal__doc :global(p),
  .legal__doc :global(li) {
    font-size: 14.5px;
    line-height: 1.65;
    color: var(--text-2);
  }
  .legal__doc :global(ul),
  .legal__doc :global(ol) { padding-left: 22px; }
  .legal__doc :global(li) { margin: 4px 0; }
  .legal__doc :global(a) { color: var(--accent); }
  .legal__doc :global(code) {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 0.9em;
    background: var(--bg-subtle);
    padding: 1px 4px;
    border-radius: 3px;
  }
  .legal__doc :global(hr) {
    border: 0;
    border-top: 1px solid var(--border-mid);
    margin: 28px 0;
  }
  .legal__doc :global(table) { border-collapse: collapse; width: 100%; font-size: 14px; }
  .legal__doc :global(th),
  .legal__doc :global(td) {
    border: 1px solid var(--border-mid);
    padding: 6px 10px;
    text-align: left;
  }
</style>
