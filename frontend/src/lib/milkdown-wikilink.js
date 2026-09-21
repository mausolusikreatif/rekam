// ProseMirror plugin: [[ autocomplete for wiki-links inside the Milkdown editor.
//
// Typing `[[` opens a floating picker of memory titles. Typing a relation prefix
// (`depends-on::…`) filters by the remaining title text and is preserved on
// insert. Arrow keys / Enter / Tab select; Escape dismisses.

import { Plugin, PluginKey } from '@milkdown/prose/state';

const RELS = ['relates', 'depends-on', 'supersedes', 'contradicts'];
const MAX_ITEMS = 8;

// Find an open `[[…` immediately before the cursor (no closing ]] or stray
// brackets between). Returns the trigger span and parsed rel/query, or null.
function activeTrigger(state) {
  const { $from, empty } = state.selection;
  if (!empty) return null;
  const textBefore = $from.parent.textBetween(
    0, $from.parentOffset, undefined, '￼'
  );
  const m = /\[\[([^[\]]*)$/.exec(textBefore);
  if (!m) return null;

  const inner = m[1];
  const to = $from.pos;
  const from = to - m[0].length; // position of the first '['

  let relPrefix = '';
  let query = inner;
  const sep = inner.indexOf('::');
  if (sep >= 0) {
    relPrefix = inner.slice(0, sep + 2); // e.g. "depends-on::"
    query = inner.slice(sep + 2);
  }
  return { from, to, relPrefix, query };
}

class AutocompleteView {
  constructor(view, getTitles) {
    this.view = view;
    this.getTitles = getTitles;
    this.items = [];
    this.index = 0;
    this.trigger = null;

    this.dom = document.createElement('div');
    this.dom.className = 'wikilink-suggest';
    this.dom.style.display = 'none';
    document.body.appendChild(this.dom);

    this.onKeyDown = this.onKeyDown.bind(this);
    // Capture phase so we intercept nav keys before ProseMirror handles them.
    view.dom.addEventListener('keydown', this.onKeyDown, true);

    this.update(view);
  }

  onKeyDown(e) {
    if (!this.trigger || this.items.length === 0) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault(); e.stopPropagation();
      this.index = (this.index + 1) % this.items.length;
      this.render();
    } else if (e.key === 'ArrowUp') {
      e.preventDefault(); e.stopPropagation();
      this.index = (this.index - 1 + this.items.length) % this.items.length;
      this.render();
    } else if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault(); e.stopPropagation();
      this.choose(this.items[this.index]);
    } else if (e.key === 'Escape') {
      e.preventDefault(); e.stopPropagation();
      this.hide();
    }
  }

  choose(title) {
    if (!this.trigger) return;
    const { from, to, relPrefix } = this.trigger;
    const text = `[[${relPrefix}${title}]] `;
    const tr = this.view.state.tr.insertText(text, from, to);
    this.view.dispatch(tr);
    this.hide();
    this.view.focus();
  }

  hide() {
    this.trigger = null;
    this.items = [];
    this.dom.style.display = 'none';
  }

  update(view) {
    const trigger = activeTrigger(view.state);
    if (!trigger) { this.hide(); return; }

    const q = trigger.query.trim().toLowerCase();
    const all = this.getTitles() || [];
    let items = all.filter(t => t && t.toLowerCase().includes(q));
    // Prefix matches first, then the rest; de-dupe; cap.
    items.sort((a, b) => {
      const ap = a.toLowerCase().startsWith(q) ? 0 : 1;
      const bp = b.toLowerCase().startsWith(q) ? 0 : 1;
      return ap - bp || a.localeCompare(b);
    });
    items = [...new Set(items)].slice(0, MAX_ITEMS);
    if (items.length === 0) { this.hide(); return; }

    this.trigger = trigger;
    this.items = items;
    if (this.index >= items.length) this.index = 0;
    this.render();
    this.position();
  }

  render() {
    const relLabel = this.trigger.relPrefix
      ? this.trigger.relPrefix.replace('::', '')
      : '';
    this.dom.innerHTML = '';
    if (relLabel && RELS.includes(relLabel)) {
      const hdr = document.createElement('div');
      hdr.className = 'wikilink-suggest__hdr';
      hdr.textContent = relLabel;
      this.dom.appendChild(hdr);
    }
    this.items.forEach((title, i) => {
      const row = document.createElement('div');
      row.className = 'wikilink-suggest__item' + (i === this.index ? ' is-active' : '');
      row.textContent = title;
      row.addEventListener('mousedown', (e) => {
        e.preventDefault();
        this.choose(title);
      });
      row.addEventListener('mouseenter', () => {
        this.index = i;
        this.render();
      });
      this.dom.appendChild(row);
    });
    this.dom.style.display = 'block';
  }

  position() {
    try {
      const coords = this.view.coordsAtPos(this.trigger.to);
      this.dom.style.left = `${coords.left}px`;
      this.dom.style.top = `${coords.bottom + 4}px`;
    } catch (_) { /* position best-effort */ }
  }

  destroy() {
    this.view.dom.removeEventListener('keydown', this.onKeyDown, true);
    this.dom.remove();
  }
}

export function wikiLinkAutocomplete(getTitles) {
  return new Plugin({
    key: new PluginKey('rekam-wikilink-autocomplete'),
    view: (editorView) => new AutocompleteView(editorView, getTitles),
  });
}
