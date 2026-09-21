export const PAGE_SIZE = 10;

export function formatDate(iso) {
  if (!iso) return '—';
  const d = new Date(iso);
  const diff = Date.now() - d;
  if (diff < 60000) return 'just now';
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
  if (diff < 7 * 86400000) return `${Math.floor(diff / 86400000)}d ago`;
  return d.toLocaleDateString('en', { month: 'short', day: 'numeric', year: diff > 365 * 86400000 ? 'numeric' : undefined });
}

export function formatFullDate(iso) {
  if (!iso) return '—';
  return new Date(iso).toLocaleString('en', {
    month: 'short', day: 'numeric', year: 'numeric',
    hour: 'numeric', minute: '2-digit',
  });
}

export function initials(name) {
  return (name || '?').split(/\s+/).map(w => w[0]).join('').slice(0, 2).toUpperCase();
}

// Build a two-level tree from flat catalog entries.
// e.g. [{path:'a.b', count:3}, {path:'a.c', count:2}]
// → [{label:'a', path:'a', count:5, children:[{label:'b', path:'a.b', count:3}, ...]}]
export function buildTree(entries) {
  const roots = new Map();
  for (const entry of entries) {
    const dot = entry.path.indexOf('.');
    const rootLabel = dot === -1 ? entry.path : entry.path.slice(0, dot);
    if (!roots.has(rootLabel)) {
      roots.set(rootLabel, { label: rootLabel, path: rootLabel, count: 0, children: [] });
    }
    const root = roots.get(rootLabel);
    root.count += entry.count;
    if (dot !== -1) {
      root.children.push({ label: entry.path.slice(dot + 1), path: entry.path, count: entry.count });
    }
  }
  return Array.from(roots.values());
}

export function debounce(fn, ms) {
  let t;
  return (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms); };
}

// Trigger a browser download for an in-memory Blob.
export function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
