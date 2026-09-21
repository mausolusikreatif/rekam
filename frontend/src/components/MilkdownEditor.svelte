<script>
  import { onMount, onDestroy } from 'svelte';
  import { Editor, rootCtx, defaultValueCtx, prosePluginsCtx, editorViewCtx } from '@milkdown/core';
  import { commonmark } from '@milkdown/preset-commonmark';
  import { gfm } from '@milkdown/preset-gfm';
  import { history } from '@milkdown/plugin-history';
  import { listener, listenerCtx } from '@milkdown/plugin-listener';
  import { getMarkdown, callCommand } from '@milkdown/utils';
  import { insertTableCommand } from '@milkdown/preset-gfm';
  import { Plugin, PluginKey } from '@milkdown/prose/state';
  import { Decoration, DecorationSet } from '@milkdown/prose/view';
  import { wikiLinkAutocomplete } from '../lib/milkdown-wikilink.js';
  import { imageUpload, insertImageFromFile } from '../lib/milkdown-image-upload.js';
  import { apiUploadFile } from '../lib/api.js';
  import { parseVideoUrl } from '../lib/video-embed.js';
  import { tableBlock, tableBlockConfig } from '@milkdown/components/table-block';
  import { math, katexOptionsCtx } from '@milkdown/plugin-math';
  import 'katex/dist/katex.min.css';

  // Concise glyphs for the interactive table controls (defaults are text like
  // "left"/"center"). Styled via the .milkdown table-wrapper CSS below.
  const tableButtonGlyph = (type) => ({
    col_drag_handle: '⠿', row_drag_handle: '⠿',
    add_row: '+', add_col: '+',
    delete_row: '×', delete_col: '×',
    align_col_left: '◧', align_col_center: '▣', align_col_right: '◨',
  })[type] ?? '';

  export let content = '';
  export let placeholder = 'Start writing…';
  // Memory titles for the [[ autocomplete picker. Read live via a getter so
  // the plugin always sees the latest list.
  export let titles = [];
  $: titlesRef = titles;

  let container;
  let editor;

  // Shows `placeholder` on the empty first paragraph (styled via the
  // .is-editor-empty[data-placeholder]::before rule below).
  function placeholderPlugin(text) {
    return new Plugin({
      key: new PluginKey('rekam-placeholder'),
      props: {
        decorations(state) {
          const { doc } = state;
          const empty = doc.childCount === 1
            && doc.firstChild?.isTextblock
            && doc.firstChild.content.size === 0;
          if (!empty) return null;
          const node = Decoration.node(0, doc.firstChild.nodeSize, {
            class: 'is-editor-empty',
            'data-placeholder': text,
          });
          return DecorationSet.create(doc, [node]);
        },
      },
    });
  }
  // Math nodes (from @milkdown/plugin-math) are rendered as KaTeX atoms, so the
  // LaTeX source isn't reachable by normal editing once rendered. This plugin
  // makes clicking a formula reopen its source in a prompt; an emptied value
  // deletes the node.
  function mathClickEdit() {
    return new Plugin({
      key: new PluginKey('rekam-math-edit'),
      props: {
        handleClickOn(view, _pos, node, nodePos, _event, direct) {
          if (!direct) return false;
          const isBlock = node.type.name === 'math_block';
          if (!isBlock && node.type.name !== 'math_inline') return false;
          const current = isBlock ? node.attrs.value : node.textContent;
          const next = window.prompt('Edit LaTeX:', current);
          if (next == null) return true;
          const { state } = view;
          let tr;
          if (!next.trim()) {
            tr = state.tr.delete(nodePos, nodePos + node.nodeSize);
          } else if (isBlock) {
            tr = state.tr.setNodeMarkup(nodePos, undefined, { value: next });
          } else {
            const replacement = node.type.create(null, state.schema.text(next));
            tr = state.tr.replaceWith(nodePos, nodePos + node.nodeSize, replacement);
          }
          view.dispatch(tr);
          return true;
        },
      },
    });
  }

  // keep a local copy updated by the listener so getValue() works even if action fails
  let latestMarkdown = content;

  let uploadError = '';
  let fileInput;   // hidden <input type=file> for the toolbar image button

  function onUploadError(err) {
    uploadError = err?.error || err?.message || 'Upload failed';
    setTimeout(() => (uploadError = ''), 4000);
  }

  // Run `fn(view)` with the live ProseMirror view, or no-op if not ready.
  function withView(fn) {
    try { editor?.action((ctx) => fn(ctx.get(editorViewCtx))); } catch (_) { /* not ready */ }
  }

  function pickImage() {
    fileInput?.click();
  }
  function onFilePicked(e) {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    withView((view) => insertImageFromFile(view, file, apiUploadFile, onUploadError));
  }

  function insertTable() {
    // gfm's InsertTable command builds a header row + (row-1) body rows.
    try { editor?.action(callCommand(insertTableCommand.key, { row: 3, col: 3 })); } catch (_) { /* not ready */ }
    withView((view) => view.focus());
  }

  function embedVideo() {
    const url = window.prompt('Paste a YouTube or Vimeo URL:');
    if (!url) return;
    if (!parseVideoUrl(url)) {
      onUploadError({ error: 'Not a recognised YouTube or Vimeo URL' });
      return;
    }
    // Insert the URL as its own paragraph, as a link node. A bare text URL would
    // be backslash-escaped by the commonmark serializer (https\://…\_), breaking
    // both autolinking and the preview's URL parser; a link keeps the href clean.
    withView((view) => {
      const { state } = view;
      const href = url.trim();
      const link = state.schema.marks.link.create({ href });
      const text = state.schema.text(href, [link]);
      const para = state.schema.nodes.paragraph.create(null, text);
      view.dispatch(state.tr.replaceSelectionWith(para));
      view.focus();
    });
  }

  function insertMath() {
    const src = window.prompt('LaTeX (display math), e.g. \\frac{a}{b}:');
    if (!src || !src.trim()) return;
    withView((view) => {
      const { state } = view;
      const node = state.schema.nodes.math_block.create({ value: src.trim() });
      view.dispatch(state.tr.replaceSelectionWith(node));
      view.focus();
    });
  }

  onMount(async () => {
    editor = await Editor.make()
      .config(ctx => {
        ctx.set(rootCtx, container);
        ctx.set(defaultValueCtx, content || '');
      })
      .config(ctx => {
        ctx.get(listenerCtx).markdownUpdated((_ctx, markdown) => {
          latestMarkdown = markdown;
        });
      })
      .config(ctx => {
        ctx.set(prosePluginsCtx, [
          ...ctx.get(prosePluginsCtx),
          placeholderPlugin(placeholder),
          mathClickEdit(),
          wikiLinkAutocomplete(() => titlesRef),
          imageUpload(apiUploadFile, onUploadError),
        ]);
      })
      .config(ctx => {
        ctx.set(tableBlockConfig.key, { renderButton: tableButtonGlyph });
      })
      .config(ctx => {
        // Bad LaTeX must render as red source text, not throw inside toDOM.
        ctx.set(katexOptionsCtx.key, { throwOnError: false });
      })
      .use(commonmark)
      .use(gfm)
      .use(math)
      .use(tableBlock)
      .use(history)
      .use(listener)
      .create();
  });

  onDestroy(() => {
    editor?.destroy();
  });

  export function getValue() {
    try {
      return unescapeWikilinks(normalizeTableCells(editor?.action(getMarkdown()) ?? latestMarkdown));
    } catch (_) {
      return unescapeWikilinks(normalizeTableCells(latestMarkdown));
    }
  }

  // The commonmark serializer escapes a literal `[` as `\[` wherever it isn't
  // already markdown link syntax, to guard against it being misread on the next
  // parse. That's correct for normal text, but it also mangles our `[[Title]]` /
  // `[[rel::Title]]` wikilink syntax into `\[\[Title]]`, which the backend's
  // wikiLinkRE (internal/db/links.go) no longer matches — so a link that resolved
  // fine one round trip is silently dropped from the graph the next time this
  // editor saves it, even for edits that never touched the link text itself.
  // Undo the escape, but only where it's part of a doubled bracket (the wikilink
  // shape) so a genuinely escaped single `\[` in prose is left alone.
  function unescapeWikilinks(md) {
    if (!md || md.indexOf('\\[') === -1) return md;
    return md.replace(/\\\[(?=\[)/g, '[').replace(/\\\](?=\])/g, ']');
  }

  // Milkdown emits a literal `<br />` for two artifacts: an *empty* table cell,
  // and an *empty paragraph*. Neither is meaningful markdown, so strip them:
  //  - in a pipe-table row, blank any cell whose whole content is just the break;
  //  - drop a line that is nothing but a break (an empty paragraph).
  // Scoped so a hard break typed *alongside* text (inline in a line) is kept.
  function normalizeTableCells(md) {
    if (!md || !/<br\s*\/?>/i.test(md)) return md;
    const isRow = (l) => /^\s*\|.*\|\s*$/.test(l);
    const onlyBreak = (l) => /^\s*<br\s*\/?>\s*$/i.test(l);
    return md.split('\n').map((line) => {
      if (onlyBreak(line)) return '';
      if (!isRow(line)) return line;
      return line.replace(/(\|)([^|]*)(?=\|)/g, (m, bar, cell) =>
        /^\s*<br\s*\/?>\s*$/i.test(cell) ? bar + '  ' : m
      );
    }).join('\n').replace(/\n{3,}/g, '\n\n').replace(/^\n+/, '');
  }
</script>

<div class="editor-toolbar">
  <button type="button" class="tb-btn" on:click={pickImage} title="Insert image">
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><path d="m21 15-5-5L5 21"/>
    </svg>
    Image
  </button>
  <button type="button" class="tb-btn" on:click={insertTable} title="Insert a table">
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M3 15h18M9 3v18M15 3v18"/>
    </svg>
    Table
  </button>
  <button type="button" class="tb-btn" on:click={embedVideo} title="Embed a YouTube or Vimeo video">
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="2" y="4" width="20" height="16" rx="2"/><path d="m10 9 5 3-5 3z" fill="currentColor" stroke="none"/>
    </svg>
    Video
  </button>
  <button type="button" class="tb-btn" on:click={insertMath} title="Insert LaTeX math (or type $x$ inline, $$ for a block)">
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M5 4h14M5 4l8 8-8 8M5 20h14"/>
    </svg>
    Math
  </button>
  {#if uploadError}<span class="tb-error">{uploadError}</span>{/if}
</div>
<input type="file" accept="image/png,image/jpeg,image/gif,image/webp" bind:this={fileInput} on:change={onFilePicked} hidden />
<div bind:this={container} class="milkdown-wrap"></div>

<style>
  /* ── Toolbar ──────────────────────────────────────────────────────────────── */
  .editor-toolbar {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: .6em;
    padding-bottom: .5em;
    border-bottom: 1px solid var(--border);
  }
  .tb-btn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-family: var(--font-mono);
    font-size: 11.5px;
    letter-spacing: .03em;
    color: var(--text-2);
    background: transparent;
    border: 1px solid var(--border-mid);
    border-radius: var(--radius);
    padding: 4px 9px;
    cursor: pointer;
    transition: color .12s, background .12s, border-color .12s;
  }
  .tb-btn:hover { color: var(--text); background: var(--bg-hover); border-color: var(--border-focus); }
  .tb-btn svg { color: var(--text-3); }
  .tb-btn:hover svg { color: var(--accent); }
  .tb-error {
    font-size: 12px;
    color: var(--stamp);
    margin-left: 4px;
  }

  /* ── Milkdown container reset ─────────────────────────────────────────────── */
  :global(.milkdown-wrap .milkdown) {
    all: unset;
    display: block;
  }

  /* ── Images ───────────────────────────────────────────────────────────────── */
  :global(.milkdown-wrap .editor img) {
    max-width: 100%;
    height: auto;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    margin: .4em 0;
    display: block;
  }

  :global(.milkdown-wrap .editor) {
    outline: none;
    min-height: 320px;
    font-family: var(--font);
    font-size: 17px;
    line-height: 1.72;
    color: var(--text);
    caret-color: var(--accent);
  }

  /* ── Prose elements ──────────────────────────────────────────────────────── */
  :global(.milkdown-wrap .editor > *:first-child) { margin-top: 0; }
  :global(.milkdown-wrap .editor > *:last-child)  { margin-bottom: 0; }

  :global(.milkdown-wrap .editor p) { margin: 0 0 .85em; }

  :global(.milkdown-wrap .editor h1),
  :global(.milkdown-wrap .editor h2),
  :global(.milkdown-wrap .editor h3),
  :global(.milkdown-wrap .editor h4) { font-family: var(--font-display); letter-spacing: -.01em; color: var(--text); }
  :global(.milkdown-wrap .editor h1) { font-size: 1.5em; font-weight: 540; margin: 1.6em 0 .5em; padding-bottom: .25em; border-bottom: 1px solid var(--border); }
  :global(.milkdown-wrap .editor h2) { font-size: 1.28em; font-weight: 540; margin: 1.5em 0 .4em; }
  :global(.milkdown-wrap .editor h3) { font-size: 1.1em; font-weight: 560; margin: 1.3em 0 .3em; }
  :global(.milkdown-wrap .editor h4) { font-size: 1em; font-weight: 600; margin: 1.1em 0 .3em; color: var(--text-2); }

  :global(.milkdown-wrap .editor a) { color: var(--accent); text-decoration: underline; text-decoration-color: var(--border-mid); text-underline-offset: 2px; }
  :global(.milkdown-wrap .editor a:hover) { text-decoration-color: var(--accent); }

  :global(.milkdown-wrap .editor strong) { font-weight: 640; }
  :global(.milkdown-wrap .editor em) { font-style: italic; }
  :global(.milkdown-wrap .editor del) { text-decoration: line-through; color: var(--text-3); }

  /* ── Code ─────────────────────────────────────────────────────────────────── */
  :global(.milkdown-wrap .editor code) {
    font-family: var(--font-mono);
    font-size: .82em;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: .12em .4em;
    color: var(--accent-hover);
  }
  :global(.milkdown-wrap .editor pre) {
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: 1em 1.25em;
    overflow-x: auto;
    margin: 1.1em 0;
  }
  :global(.milkdown-wrap .editor pre code) {
    background: none;
    border: none;
    padding: 0;
    font-size: .82em;
    color: var(--text);
  }

  /* ── Blockquote ───────────────────────────────────────────────────────────── */
  :global(.milkdown-wrap .editor blockquote) {
    border-left: 2px solid var(--accent);
    margin: 1.1em 0;
    padding: .3em 1.1em;
    color: var(--text-2);
    font-style: italic;
  }
  :global(.milkdown-wrap .editor blockquote p) { margin: 0; }

  /* ── Lists ────────────────────────────────────────────────────────────────── */
  :global(.milkdown-wrap .editor ul),
  :global(.milkdown-wrap .editor ol) { padding-left: 1.4em; margin: 0 0 .85em; }
  :global(.milkdown-wrap .editor li) { margin: .25em 0; }
  :global(.milkdown-wrap .editor li p) { margin: .4em 0; }
  :global(.milkdown-wrap .editor li::marker) { color: var(--text-3); }

  /* task list */
  :global(.milkdown-wrap .editor .task-list-item) { list-style: none; margin-left: -1.4em; padding-left: 1.4em; }
  :global(.milkdown-wrap .editor .task-list-item input) { margin-right: .5em; accent-color: var(--accent); }

  /* ── Table ────────────────────────────────────────────────────────────────── */
  :global(.milkdown-wrap .editor table) {
    width: 100%;
    border-collapse: collapse;
    margin: 1.1em 0;
    font-size: 14.5px;
  }
  :global(.milkdown-wrap .editor th) {
    text-align: left;
    font-family: var(--font-mono);
    font-weight: 500;
    padding: .5em .75em;
    border-bottom: 1px solid var(--border-mid);
    color: var(--text-2);
    font-size: .78em;
    letter-spacing: .04em;
    text-transform: uppercase;
    background: var(--bg-subtle);
  }
  :global(.milkdown-wrap .editor td) {
    padding: .5em .75em;
    border-bottom: 1px solid var(--border);
    vertical-align: top;
  }
  :global(.milkdown-wrap .editor tr:last-child td) { border-bottom: none; }

  /* selected table cell */
  :global(.milkdown-wrap .editor .selectedCell) { background: var(--accent-wash); }

  /* ── Interactive table controls (@milkdown/components table-block) ─────────────
     The component renders drag/add/align handles as absolutely-positioned
     siblings whose left/top are set in JS; it ships unstyled, so everything below
     positions and themes them into the paper-and-ink look. */

  /* positioning contexts: the node-view root (holds the row/col drag handles) and
     the table-wrapper (holds the add-line handles + drag preview). */
  :global(.milkdown-wrap .editor div:has(> [data-role="col-drag-handle"])) { position: relative; }
  :global(.milkdown-wrap .editor .table-wrapper) { position: relative; overflow: visible; padding: 2px; }

  /* full grid lines while editing, so the table reads as an editable spreadsheet */
  :global(.milkdown-wrap .editor .table-wrapper th),
  :global(.milkdown-wrap .editor .table-wrapper td) {
    border: 1px solid var(--border);
    position: relative;
  }

  /* base handle: revealed by opacity (NOT pointer-events) so a handle the
     component has flagged data-show="false" is still reachable — moving onto it
     re-reveals it via :hover below. This is the key to the grips being grabbable;
     disabling pointer-events makes them un-clickable as you approach. */
  :global(.milkdown-wrap .editor .handle) {
    position: absolute;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 13px;
    transition: opacity .18s ease-in-out;
    user-select: none;
  }
  :global(.milkdown-wrap .editor .handle[data-show="false"]) { opacity: 0; }

  /* drag grips: a rounded pill overlapping the top of each column / left of each
     row (the translate makes it overlap the edge so it stays reachable). */
  :global(.milkdown-wrap .editor .cell-handle) {
    z-index: 50;
    left: -999px;
    top: -999px;
    cursor: grab;
    background: var(--bg-subtle);
    border: 1px solid var(--border-mid);
    color: var(--text-3);
    border-radius: 100px;
    box-shadow: var(--shadow-sm);
    transition: opacity .18s ease-in-out, background .15s, border-color .15s, color .15s;
  }
  :global(.milkdown-wrap .editor .cell-handle:hover) { opacity: 1; background: var(--bg-hover); color: var(--accent); border-color: var(--border-focus); }
  :global(.milkdown-wrap .editor .cell-handle:has(.button-group:hover)) { opacity: 1; }
  :global(.milkdown-wrap .editor .cell-handle:active) { cursor: grabbing; }
  :global(.milkdown-wrap .editor [data-role="col-drag-handle"]) { transform: translateY(50%); width: 28px; height: 15px; }
  :global(.milkdown-wrap .editor [data-role="row-drag-handle"]) { transform: translateX(50%); width: 15px; height: 28px; }
  :global(.milkdown-wrap .editor [data-role="row-drag-handle"] .milkdown-icon) { transform: rotate(90deg); }
  :global(.milkdown-wrap .editor .cell-handle .milkdown-icon) { font-size: 11px; line-height: 1; pointer-events: none; }

  /* add row / add column: a thin accent insertion line with a round + button */
  :global(.milkdown-wrap .editor .line-handle) { z-index: 20; background: var(--accent); }
  :global(.milkdown-wrap .editor .line-handle:hover) { opacity: 1; }
  :global(.milkdown-wrap .editor [data-role="x-line-drag-handle"]) { height: 1px; }
  :global(.milkdown-wrap .editor [data-role="y-line-drag-handle"]) { width: 1px; }
  :global(.milkdown-wrap .editor .line-handle[data-display-type="indicator"] .add-button) { display: none; }
  :global(.milkdown-wrap .editor .add-button) {
    position: absolute;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    padding: 0;
    border: none;
    border-radius: 50%;
    background: var(--accent);
    color: var(--accent-text, #fff);
    cursor: pointer;
    font-size: 14px;
    line-height: 1;
    box-shadow: var(--shadow-sm);
    transform: translate(-50%, -50%);
    transition: background .1s ease;
  }
  :global(.milkdown-wrap .editor .add-button:hover) { background: var(--accent-hover); }
  :global(.milkdown-wrap .editor .add-button .milkdown-icon) { pointer-events: none; }

  /* popover (alignment + delete), floated above the grip with a hover bridge so
     the pointer can travel from grip to popover without it collapsing. */
  :global(.milkdown-wrap .editor .button-group) {
    position: absolute;
    left: 50%;
    bottom: calc(100% + 7px);
    transform: translateX(-50%);
    display: flex;
    gap: 2px;
    padding: 3px;
    background: var(--bg-raised);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-md);
    z-index: 60;
  }
  :global(.milkdown-wrap .editor .button-group::after) {
    content: '';
    position: absolute;
    top: 100%;
    left: 0;
    width: 100%;
    height: 9px;
  }
  :global(.milkdown-wrap .editor .button-group[data-show="false"]) { display: none; }
  :global(.milkdown-wrap .editor .button-group button) {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    padding: 0;
    border: none;
    border-radius: var(--radius);
    background: transparent;
    color: var(--text-2);
    cursor: pointer;
    font-size: 14px;
    line-height: 1;
  }
  :global(.milkdown-wrap .editor .button-group button:hover) { background: var(--bg-hover); color: var(--accent); }

  /* drag ghost while reordering a row/column */
  :global(.milkdown-wrap .editor .drag-preview) {
    position: absolute;
    z-index: 100;
    opacity: .5;
    background: var(--bg-raised);
    outline: 1px solid var(--accent);
    outline-offset: -1px;
    box-shadow: var(--shadow-lg);
  }
  :global(.milkdown-wrap .editor .drag-preview[data-show="false"]) { display: none; }
  :global(.milkdown-wrap .editor .drag-preview table) { margin: 0; }
  :global(.milkdown-wrap .editor .drag-preview th),
  :global(.milkdown-wrap .editor .drag-preview td) { background: var(--bg-raised); }

  /* ── Math (KaTeX) ─────────────────────────────────────────────────────────── */
  :global(.milkdown-wrap .editor [data-type="math_inline"]) {
    cursor: pointer;
    border-radius: 3px;
    padding: 0 .1em;
    transition: background .12s;
  }
  :global(.milkdown-wrap .editor [data-type="math_inline"]:hover) { background: var(--accent-wash, var(--bg-hover)); }
  :global(.milkdown-wrap .editor [data-type="math_block"]) {
    display: block;
    text-align: center;
    margin: 1.1em 0;
    padding: .8em 1em;
    border: 1px solid transparent;
    border-radius: var(--radius-md);
    cursor: pointer;
    overflow-x: auto;
    transition: background .12s, border-color .12s;
  }
  :global(.milkdown-wrap .editor [data-type="math_block"]:hover) {
    background: var(--bg-subtle);
    border-color: var(--border);
  }
  /* katex error output (throwOnError: false) */
  :global(.milkdown-wrap .editor .katex-error) { color: var(--stamp); }

  /* ── HR ───────────────────────────────────────────────────────────────────── */
  :global(.milkdown-wrap .editor hr) {
    border: none;
    border-top: 1px solid var(--border-mid);
    margin: 2em 0;
  }

  /* ── Milkdown selection / placeholder ────────────────────────────────────── */
  :global(.milkdown-wrap .editor .ProseMirror-selectednode) {
    outline: 2px solid var(--accent);
    border-radius: 2px;
  }
  :global(.milkdown-wrap .editor p.is-editor-empty:first-child::before) {
    content: attr(data-placeholder);
    color: var(--text-3);
    pointer-events: none;
    float: left;
    height: 0;
  }

  /* ── Milkdown toolbar (if any plugin adds one) ────────────────────────────── */
  :global(.milkdown-wrap .milkdown-menu) {
    background: var(--bg-raised);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-md);
  }
  :global(.milkdown-wrap .milkdown-menu button) {
    color: var(--text-2);
    background: transparent;
    border: none;
    border-radius: 4px;
    padding: 4px 6px;
    cursor: pointer;
  }
  :global(.milkdown-wrap .milkdown-menu button:hover) { color: var(--text); background: var(--bg-hover); }
  :global(.milkdown-wrap .milkdown-menu button.active) { color: var(--accent); }

  /* ── [[ wiki-link autocomplete popup (appended to <body>) ─────────────────── */
  :global(.wikilink-suggest) {
    position: fixed;
    z-index: 1000;
    min-width: 220px;
    max-width: 360px;
    background: var(--bg-raised);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-md);
    padding: 4px;
    overflow: hidden;
  }
  :global(.wikilink-suggest__hdr) {
    font-family: var(--font-mono);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: .1em;
    text-transform: uppercase;
    color: var(--accent-hover);
    padding: 4px 8px 3px;
  }
  :global(.wikilink-suggest__item) {
    padding: 5px 9px;
    border-radius: var(--radius);
    font-size: 13.5px;
    color: var(--text);
    cursor: pointer;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  :global(.wikilink-suggest__item.is-active) {
    background: var(--accent-wash, var(--bg-hover));
    color: var(--accent-hover);
  }
</style>
