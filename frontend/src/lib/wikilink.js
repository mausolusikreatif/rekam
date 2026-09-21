// Wiki-link rendering for memory content.
//
// Turns [[Title]] and [[rel::Title]] markers into styled, clickable chips. The
// relation vocabulary mirrors the backend (internal/db/links.go): a bare link is
// `relates`; an unknown prefix degrades to `relates`.

const VALID_RELS = new Set(['relates', 'depends-on', 'supersedes', 'contradicts']);

function esc(s) {
  return String(s).replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}

// parseInner splits the text between [[ ]] into a relation and a display title.
export function parseInner(inner) {
  const i = inner.indexOf('::');
  if (i >= 0) {
    const cand = inner.slice(0, i).trim().toLowerCase();
    if (VALID_RELS.has(cand)) {
      return { rel: cand, title: inner.slice(i + 2).trim() };
    }
  }
  return { rel: 'relates', title: inner.trim() };
}

// A marked inline extension. Registered after the default tokenizers, so [[...]]
// inside code spans / fenced blocks is left untouched (those tokens win first).
export const wikiLinkExtension = {
  name: 'wikiLink',
  level: 'inline',
  start(src) {
    const i = src.indexOf('[[');
    return i < 0 ? undefined : i;
  },
  tokenizer(src) {
    // Exclude '[' from the target so a stray '[[' can't span across a later link.
    const m = /^\[\[([^\[\]]+?)\]\]/.exec(src);
    if (!m) return undefined;
    const { rel, title } = parseInner(m[1]);
    if (!title) return undefined;
    return { type: 'wikiLink', raw: m[0], rel, title };
  },
  renderer(token) {
    const { rel, title } = token;
    const badge = rel !== 'relates'
      ? `<span class="wikilink__rel">${esc(rel)}</span>`
      : '';
    return `<a class="wikilink" data-wikilink="${esc(title)}" data-rel="${esc(rel)}" title="${esc(rel)} → ${esc(title)}">`
      + `<span class="wikilink__icon" aria-hidden="true">↗</span>`
      + badge
      + `<span class="wikilink__title">${esc(title)}</span></a>`;
  },
};

// DOMPurify hook config: data-* and class survive by default, but be explicit so
// the chip attributes are never stripped.
export const wikiLinkPurifyConfig = { ADD_ATTR: ['data-wikilink', 'data-rel'] };
