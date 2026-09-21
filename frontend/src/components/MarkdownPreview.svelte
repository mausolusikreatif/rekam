<script>
  import { marked } from 'marked';
  import markedKatex from 'marked-katex-extension';
  import 'katex/dist/katex.min.css';
  import DOMPurify from 'dompurify';
  import mermaid from 'mermaid';
  import { tick } from 'svelte';
  import { wikiLinkExtension, wikiLinkPurifyConfig } from '../lib/wikilink.js';
  import { parseVideoUrl } from '../lib/video-embed.js';
  import { mediaKind, isSameOrigin, mediaHost } from '../lib/media-embed.js';
  import { apiSearch } from '../lib/api.js';
  import { focusLink, docsMode } from '../lib/store.js';
  import { openDocsRecord } from '../lib/docs-route.js';
  import { get } from 'svelte/store';

  export let content = '';
  // Content type discriminator from the backend: markdown (default), mermaid,
  // code, or table. Drives how the body is rendered.
  export let format = 'markdown';

  marked.setOptions({ gfm: true, breaks: false });
  marked.use({ extensions: [wikiLinkExtension] });
  // $x$ inline / $$…$$ display math, rendered by KaTeX. Bad LaTeX renders as
  // red source instead of throwing; the KaTeX HTML survives DOMPurify (MathML
  // tags are in its default allowlist).
  marked.use(markedKatex({ throwOnError: false }));

  // mermaid renders its own SVG; securityLevel 'strict' sanitizes the input.
  mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: 'neutral' });
  let diagId = 0;

  $: norm_format = (format || 'markdown').toLowerCase();
  $: isMarkdown = norm_format === 'markdown';

  // ── markdown ──────────────────────────────────────────────────────────────
  $: rendered = isMarkdown && content
    ? DOMPurify.sanitize(marked.parse(content), wikiLinkPurifyConfig)
    : '';
  // After markdown renders, upgrade any ```mermaid fenced blocks into diagrams,
  // so diagrams embedded in a markdown note render too (not just format=mermaid).
  $: if (isMarkdown) upgradeMermaidFences(rendered);
  // …and turn bare YouTube/Vimeo URLs (each on their own line) into players.
  $: if (isMarkdown) upgradeVideoEmbeds(rendered);
  // …and bare image/clip URLs into the thing they point at.
  $: if (isMarkdown) upgradeMediaLinks(rendered);
  // …and mark every externally-hosted image, so a dead link reads as a dead
  // link rather than as a rendering bug.
  $: if (isMarkdown) markExternalImages(rendered);

  // ── mermaid (whole body is one diagram) ─────────────────────────────────────
  let mermaidSvg = '';
  let mermaidErr = '';
  $: if (norm_format === 'mermaid') renderMermaid(content);
  async function renderMermaid(src) {
    if (!src || !src.trim()) { mermaidSvg = ''; mermaidErr = ''; return; }
    try {
      const { svg } = await mermaid.render('mmd-' + (++diagId), src.trim());
      mermaidSvg = svg; mermaidErr = '';
    } catch (e) {
      mermaidSvg = ''; mermaidErr = String(e?.message || e);
    }
  }

  // Replace a paragraph that is just a YouTube/Vimeo link with a sandboxed
  // iframe player. The iframe is built here from a validated embed URL (the
  // video id is charset-checked in parseVideoUrl) and injected as a real DOM
  // node — it never passes through the HTML string / sanitizer, so no
  // attacker-controlled markup can ride along.
  async function upgradeVideoEmbeds(_r) {
    await tick();
    if (!bodyEl) return;
    for (const a of bodyEl.querySelectorAll('p > a')) {
      const p = a.parentElement;
      // Only when the link is the sole content of its paragraph.
      if (p.childNodes.length !== 1) continue;
      const video = parseVideoUrl(a.getAttribute('href') || '');
      if (!video) continue;

      const wrap = document.createElement('div');
      wrap.className = 'video-embed';
      const frame = document.createElement('iframe');
      frame.src = video.embedUrl;
      frame.loading = 'lazy';
      frame.setAttribute('allow', 'accelerometer; encrypted-media; picture-in-picture; fullscreen');
      frame.setAttribute('allowfullscreen', '');
      frame.setAttribute('sandbox', 'allow-scripts allow-same-origin allow-presentation allow-popups');
      frame.setAttribute('referrerpolicy', 'strict-origin-when-cross-origin');
      frame.title = video.provider + ' video player';
      wrap.appendChild(frame);
      p.replaceWith(wrap);
    }
  }

  // A bare image or clip URL alone in a paragraph becomes the media itself,
  // the same way a bare YouTube link already becomes a player. Writing
  // ![](url) works too and always has; this is for the far more common act of
  // pasting a URL and expecting to see the picture.
  //
  // Elements are built as DOM nodes from a URL the sanitizer already passed,
  // rather than by splicing HTML — same reasoning as upgradeVideoEmbeds.
  async function upgradeMediaLinks(_r) {
    await tick();
    if (!bodyEl) return;
    for (const a of bodyEl.querySelectorAll('p > a')) {
      const p = a.parentElement;
      if (p.childNodes.length !== 1) continue;
      const href = a.getAttribute('href') || '';
      // Only when the link's text is the URL itself. A deliberately worded
      // link ("[the diagram](…png)") is a link the author wrote on purpose.
      if (a.textContent.trim() !== href.trim()) continue;
      const kind = mediaKind(href);
      if (!kind) continue;

      if (kind === 'image') {
        const img = document.createElement('img');
        img.src = href;
        img.alt = '';
        applyExternalImagePolicy(img, href);
        p.replaceWith(wrapMedia(img, href));
      } else {
        const video = document.createElement('video');
        video.src = href;
        video.controls = true;
        video.preload = 'none';
        video.setAttribute('referrerpolicy', 'no-referrer');
        video.className = 'media-embed__video';
        p.replaceWith(wrapMedia(video, href));
      }
    }
  }

  function wrapMedia(el, href) {
    const wrap = document.createElement('div');
    wrap.className = 'media-embed';
    if (!isSameOrigin(href)) wrap.classList.add('media-embed--external');
    wrap.appendChild(el);
    return wrap;
  }

  // Every image the record links to rather than contains. rekam stores
  // records, not blobs, so this fetch happens in the reader's browser against
  // someone else's server: it can be gone, and it can be visible to the author
  // and not to a teammate whose account cannot see that Drive file.
  //
  // Two consequences handled here. no-referrer, because otherwise opening a
  // record tells that server which page the reader was on. And an onerror
  // fallback, because the default broken-image icon reads as rekam failing.
  function applyExternalImagePolicy(img, href) {
    img.loading = 'lazy';
    img.decoding = 'async';
    if (isSameOrigin(href)) return;
    img.setAttribute('referrerpolicy', 'no-referrer');
    img.addEventListener('error', () => replaceWithDeadLink(img, href), { once: true });
  }

  // What a reader sees instead of a broken image: what was meant to be here,
  // where it lives, and a way to go look. Some hosts (Google Drive and
  // Dropbox share links above all) serve a web page rather than an image and
  // can never render inline — for those this is the correct final state, not
  // a failure.
  function replaceWithDeadLink(img, href) {
    const host = mediaHost(href);
    const card = document.createElement('div');
    card.className = 'media-dead';
    const label = document.createElement('span');
    label.className = 'media-dead__label';
    label.textContent = host
      ? `Image hosted on ${host} — it did not load`
      : 'Image did not load';
    const link = document.createElement('a');
    link.href = href;
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    link.className = 'media-dead__link';
    link.textContent = 'Open it';
    const note = document.createElement('span');
    note.className = 'media-dead__note';
    note.textContent = 'rekam links to it; it is not stored here.';
    card.append(label, link, note);
    // Replace the whole block, not just the <img>: a markdown image sits in
    // its own <p>, and putting a <div> inside one makes the browser split the
    // paragraph around it.
    const wrapper = img.closest('.media-embed');
    const para = img.parentElement;
    const target =
      wrapper ||
      (para && para.tagName === 'P' && para.childNodes.length === 1 ? para : img);
    target.replaceWith(card);
  }

  // Images written as ![](url) go through the sanitizer as HTML, so they never
  // pass applyExternalImagePolicy on the way in. Catch them after the fact.
  async function markExternalImages(_r) {
    await tick();
    if (!bodyEl) return;
    for (const img of bodyEl.querySelectorAll('img')) {
      if (img.dataset.mediaChecked) continue;
      img.dataset.mediaChecked = '1';
      applyExternalImagePolicy(img, img.getAttribute('src') || '');
    }
  }

  async function upgradeMermaidFences(_r) {
    await tick();
    if (!bodyEl) return;
    const blocks = bodyEl.querySelectorAll('code.language-mermaid');
    for (const code of blocks) {
      try {
        const { svg } = await mermaid.render('mmd-' + (++diagId), code.textContent.trim());
        const wrap = document.createElement('div');
        wrap.className = 'mermaid-diagram';
        wrap.innerHTML = svg;
        (code.closest('pre') || code).replaceWith(wrap);
      } catch (_) { /* leave the source block as-is on parse error */ }
    }
  }

  // ── table (CSV) ─────────────────────────────────────────────────────────────
  $: csvRows = norm_format === 'table' ? parseCSV(content) : null;
  function parseCSV(text) {
    const lines = String(text || '').trim().split(/\r?\n/).filter((l) => l.length);
    return lines.map((line) => {
      const cells = [];
      let cur = '', inQ = false;
      for (let i = 0; i < line.length; i++) {
        const c = line[i];
        if (inQ) {
          if (c === '"' && line[i + 1] === '"') { cur += '"'; i++; }
          else if (c === '"') inQ = false;
          else cur += c;
        } else if (c === '"') inQ = true;
        else if (c === ',') { cells.push(cur); cur = ''; }
        else cur += c;
      }
      cells.push(cur);
      return cells;
    });
  }

  // When the link-health badge sends us here to fix a broken link, scroll to the
  // matching chip and flash it. Match normalized (the badge target is lowercased).
  let bodyEl;
  const norm = (s) => String(s || '').replace(/\s+/g, ' ').trim().toLowerCase();
  $: jumpToLink(rendered, $focusLink);
  async function jumpToLink(_rendered, target) {
    if (!target) return;
    await tick();
    if (!bodyEl) return;
    const want = norm(target);
    for (const chip of bodyEl.querySelectorAll('.wikilink')) {
      if (norm(chip.getAttribute('data-wikilink')) === want) {
        chip.scrollIntoView({ behavior: 'smooth', block: 'center' });
        chip.classList.add('wikilink--flash');
        setTimeout(() => chip.classList.remove('wikilink--flash'), 2200);
        break;
      }
    }
    focusLink.set(null);
  }

  // Resolve a wiki-link click (by title) to a memory and navigate to it. Done
  // lazily here rather than at render time so the preview needs no link index.
  let resolving = false;
  async function onClick(e) {
    const el = e.target.closest?.('.wikilink');
    if (!el) return;
    e.preventDefault();
    if (resolving) return;
    const title = el.getAttribute('data-wikilink');
    if (!title) return;
    el.classList.remove('wikilink--missing');
    resolving = true;
    el.classList.add('wikilink--loading');
    try {
      const data = await apiSearch(title);
      const want = title.trim().toLowerCase();
      const hit = (data.results || []).find(m => (m.title || '').trim().toLowerCase() === want);
      if (hit) {
        // Same click, two destinations: the editor in the app, the read-only
        // reader on the public docs corpus, which routes on the path so the
        // URL stays shareable.
        if (get(docsMode)) openDocsRecord(hit);
        else window.location.hash = `/memory/${hit.id}`;
      } else {
        el.classList.add('wikilink--missing');
        el.setAttribute('title', `No memory titled "${title}" yet`);
      }
    } catch (_) {
      el.classList.add('wikilink--missing');
    } finally {
      resolving = false;
      el.classList.remove('wikilink--loading');
    }
  }
</script>

{#if norm_format === 'mermaid'}
  {#if mermaidSvg}
    <div class="markdown-body"><div class="mermaid-diagram">{@html mermaidSvg}</div></div>
  {:else if mermaidErr}
    <div class="markdown-body"><pre class="format-error">Diagram error: {mermaidErr}

{content}</pre></div>
  {:else}
    <div class="markdown-empty">No content</div>
  {/if}
{:else if norm_format === 'code'}
  {#if content}
    <div class="markdown-body"><pre><code>{content}</code></pre></div>
  {:else}
    <div class="markdown-empty">No content</div>
  {/if}
{:else if norm_format === 'table'}
  {#if csvRows && csvRows.length}
    <div class="markdown-body">
      <table>
        <thead><tr>{#each csvRows[0] as h}<th>{h}</th>{/each}</tr></thead>
        <tbody>
          {#each csvRows.slice(1) as row}
            <tr>{#each row as cell}<td>{cell}</td>{/each}</tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div class="markdown-empty">No content</div>
  {/if}
{:else if rendered}
  <div class="markdown-body" bind:this={bodyEl} on:click={onClick} role="presentation">{@html rendered}</div>
{:else}
  <div class="markdown-empty">No content</div>
{/if}

<style>
  .markdown-empty {
    color: var(--text-3);
    font-style: italic;
    font-size: 16px;
    padding: 8px 0;
  }

  /* ── Markdown prose — reading view (the record body) ───────────────────── */
  :global(.markdown-body) {
    font-family: var(--font);
    font-size: 17px;
    line-height: 1.72;
    color: var(--text);
    word-break: break-word;
  }

  :global(.markdown-body > *:first-child) { margin-top: 0; }
  :global(.markdown-body > *:last-child)  { margin-bottom: 0; }

  /* Images */
  :global(.markdown-body img) {
    max-width: 100%;
    height: auto;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    margin: .5em 0;
    display: block;
  }

  /* Video embeds — responsive 16:9 */
  :global(.markdown-body .video-embed) {
    position: relative;
    width: 100%;
    aspect-ratio: 16 / 9;
    margin: 1.1em 0;
    border-radius: var(--radius-md);
    overflow: hidden;
    border: 1px solid var(--border);
    background: #000;
  }
  :global(.markdown-body .video-embed iframe) {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    border: 0;
  }

  /* Media the record links to rather than contains. Sized to its own
     content rather than a fixed ratio — a screenshot is not 16:9. */
  :global(.markdown-body .media-embed) {
    margin: 1.1em 0;
  }
  :global(.markdown-body .media-embed img),
  :global(.markdown-body .media-embed__video) {
    display: block;
    max-width: 100%;
    height: auto;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
  }

  /* An image that did not load. Deliberately not styled as an error: for a
     Drive or Dropbox share link this is the correct final state, not a
     failure, and the reader's next move is the same either way — open it. */
  :global(.markdown-body .media-dead) {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 4px 10px;
    margin: 1.1em 0;
    padding: 12px 14px;
    border: 1px dashed var(--border-mid);
    border-radius: var(--radius-md);
    background: var(--bg-subtle);
    font-size: 13.5px;
  }
  :global(.markdown-body .media-dead__label) { color: var(--text-2); }
  :global(.markdown-body .media-dead__link) { color: var(--accent); }
  :global(.markdown-body .media-dead__note) {
    flex-basis: 100%;
    font-size: 12.5px;
    color: var(--text-2);
  }

  :global(.markdown-body h1),
  :global(.markdown-body h2),
  :global(.markdown-body h3),
  :global(.markdown-body h4) { font-family: var(--font-display); letter-spacing: -.01em; color: var(--text); }
  :global(.markdown-body h1) { font-size: 1.5em; font-weight: 540; margin: 1.6em 0 .5em; padding-bottom: .25em; border-bottom: 1px solid var(--border); }
  :global(.markdown-body h2) { font-size: 1.28em; font-weight: 540; margin: 1.5em 0 .4em; }
  :global(.markdown-body h3) { font-size: 1.1em; font-weight: 560; margin: 1.3em 0 .3em; }
  :global(.markdown-body h4) { font-size: 1em; font-weight: 600; margin: 1.1em 0 .3em; color: var(--text-2); }

  :global(.markdown-body p) { margin: 0 0 .85em; }

  :global(.markdown-body a) { color: var(--accent); text-decoration: underline; text-decoration-color: var(--border-mid); text-underline-offset: 2px; }
  :global(.markdown-body a:hover) { text-decoration-color: var(--accent); }

  :global(.markdown-body strong) { font-weight: 640; color: var(--text); }
  :global(.markdown-body em) { font-style: italic; }

  :global(.markdown-body code) {
    font-family: var(--font-mono);
    font-size: .82em;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: .12em .4em;
    color: var(--accent-hover);
  }

  :global(.markdown-body pre) {
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: 1em 1.25em;
    overflow-x: auto;
    margin: 1.1em 0;
  }
  :global(.markdown-body pre code) {
    background: none;
    border: none;
    padding: 0;
    font-size: .82em;
    color: var(--text);
  }

  :global(.markdown-body blockquote) {
    border-left: 2px solid var(--accent);
    margin: 1.1em 0;
    padding: .3em 1.1em;
    color: var(--text-2);
    font-style: italic;
  }
  :global(.markdown-body blockquote p) { margin: 0; }

  :global(.markdown-body ul),
  :global(.markdown-body ol) { padding-left: 1.4em; margin: 0 0 .85em; }
  :global(.markdown-body li) { margin: .25em 0; }
  :global(.markdown-body li > p) { margin: .4em 0; }
  :global(.markdown-body li::marker) { color: var(--text-3); }

  :global(.markdown-body table) {
    width: 100%;
    border-collapse: collapse;
    margin: 1.1em 0;
    font-size: 14.5px;
  }
  :global(.markdown-body th) {
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
  :global(.markdown-body td) {
    padding: .5em .75em;
    border-bottom: 1px solid var(--border);
    vertical-align: top;
  }
  :global(.markdown-body tr:last-child td) { border-bottom: none; }

  :global(.markdown-body hr) {
    border: none;
    border-top: 1px solid var(--border-mid);
    margin: 2em 0;
  }

  :global(.markdown-body img) { max-width: 100%; border-radius: var(--radius); }

  /* ── Math (KaTeX) ────────────────────────────────────────────────────────── */
  :global(.markdown-body .katex-display) {
    margin: 1.1em 0;
    overflow-x: auto;
    overflow-y: hidden;
    padding: .2em 0;
  }
  :global(.markdown-body .katex-error) { color: var(--stamp, #b4503c); }

  /* ── Diagrams (mermaid) ──────────────────────────────────────────────────── */
  :global(.markdown-body .mermaid-diagram) {
    display: flex;
    justify-content: center;
    margin: 1.2em 0;
    padding: 1em;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    overflow-x: auto;
  }
  :global(.markdown-body .mermaid-diagram svg) { max-width: 100%; height: auto; }

  :global(.markdown-body .format-error) {
    color: var(--stamp, #b4503c);
    white-space: pre-wrap;
  }

  /* ── Wiki-links — [[Title]] / [[rel::Title]] chips ───────────────────────── */
  :global(.markdown-body .wikilink) {
    display: inline-flex;
    align-items: baseline;
    gap: .3em;
    padding: .04em .5em .1em;
    margin: 0 .05em;
    border: 1px solid var(--border-mid);
    border-radius: 999px;
    background: var(--bg-subtle);
    color: var(--text);
    text-decoration: none;
    line-height: 1.3;
    cursor: pointer;
    transition: background var(--transition), border-color var(--transition), color var(--transition);
    vertical-align: baseline;
  }
  :global(.markdown-body .wikilink:hover) {
    background: var(--bg-hover);
    border-color: var(--accent);
    color: var(--accent-hover);
  }
  :global(.markdown-body .wikilink__icon) {
    font-size: .72em;
    color: var(--text-3);
    transition: color var(--transition);
  }
  :global(.markdown-body .wikilink:hover .wikilink__icon) { color: var(--accent); }
  :global(.markdown-body .wikilink__title) { font-weight: 540; }

  /* Relation badge — small uppercase tag, color-coded per relation type. */
  :global(.markdown-body .wikilink__rel) {
    font-family: var(--font-mono);
    font-size: .66em;
    font-weight: 600;
    letter-spacing: .04em;
    text-transform: uppercase;
    padding: .05em .42em;
    border-radius: 999px;
    background: var(--border);
    color: var(--text-2);
    transform: translateY(-.08em);
  }
  /* depends-on → accent (foundational) */
  :global(.markdown-body .wikilink[data-rel="depends-on"] .wikilink__rel) {
    background: var(--status-pending-bg, var(--bg-hover));
    color: var(--accent-hover);
  }
  /* supersedes → warm/stamp (this replaces something) */
  :global(.markdown-body .wikilink[data-rel="supersedes"] .wikilink__rel) {
    background: var(--status-rejected-bg, #f6e7e3);
    color: var(--stamp, #b4503c);
  }
  /* contradicts → flagged */
  :global(.markdown-body .wikilink[data-rel="contradicts"] .wikilink__rel) {
    background: var(--status-rejected-bg, #f6e7e3);
    color: var(--stamp, #b4503c);
  }

  /* States */
  :global(.markdown-body .wikilink--loading) { opacity: .55; cursor: progress; }
  :global(.markdown-body .wikilink--missing) {
    border-style: dashed;
    border-color: var(--border-mid);
    color: var(--text-3);
    text-decoration: line-through;
    text-decoration-color: var(--border-mid);
  }
  :global(.markdown-body .wikilink--missing .wikilink__icon) { display: none; }

  /* Flash when jumped-to from the link-health badge. */
  :global(.markdown-body .wikilink--flash) {
    animation: wikilink-flash 2.2s ease-out;
  }
  @keyframes wikilink-flash {
    0%, 30% {
      background: var(--accent-wash, #f3e9df);
      border-color: var(--accent);
      box-shadow: 0 0 0 3px var(--accent-wash, rgba(180,80,60,.18));
    }
    100% {
      background: var(--bg-subtle);
      box-shadow: 0 0 0 0 transparent;
    }
  }
</style>
