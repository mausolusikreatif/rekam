<script>
  import { onMount, onDestroy } from 'svelte';
  import { apiGraph, apiCreateMemory } from '../lib/api.js';
  import { canWrite } from '../lib/store.js';

  // center = memory id for an ego map, or null for the whole-corpus map.
  export let center = null;

  let canvas;
  let width = 0;
  let height = 0;
  let animId;

  let loading = true;
  let error = '';
  let graph = null;            // raw {center, nodes, edges} from the API
  let depth = 2;               // ego hop radius

  // Simulation
  let nodes = [];
  let edges = [];              // { a, b, rel } index pairs
  let idIndex = new Map();     // node id -> index
  let alpha = 1;
  const ALPHA_MIN = 0.015;
  const ALPHA_DECAY = 0.02;

  // View
  let pan = { x: 0, y: 0 };
  let scale = 1;

  // Interaction
  let dragging = null;
  let panning = null;
  let hovered = null;
  let selected = null;

  // Save / export state
  let saving = false;
  let saveMsg = '';

  // ── Relation styling ───────────────────────────────────────────────────────
  // Paper-and-ink palette; directed relations get an arrowhead.
  // Colors are resolved from the live theme at draw time (see themeColor
  // below) so the edge palette follows light/dark without a rebuild.
  function relStyles(accent, stamp) {
    return {
      'relates':     { color: `color-mix(in srgb, ${accent} 28%, transparent)`, directed: false, dashed: false },
      'depends-on':  { color: `color-mix(in srgb, ${accent} 60%, transparent)`, directed: true,  dashed: false },
      'supersedes':  { color: `color-mix(in srgb, ${stamp} 60%, transparent)`,  directed: true,  dashed: true  },
      'contradicts': { color: `color-mix(in srgb, ${stamp} 65%, transparent)`,  directed: true,  dashed: false },
    };
  }

  // ── Data load ──────────────────────────────────────────────────────────────

  async function load() {
    loading = true; error = '';
    try {
      graph = await apiGraph(center ? { center, depth } : {});
      buildGraph(graph);
    } catch (e) {
      error = (e && (e.error || e.message)) || 'Could not load mindmap';
    } finally {
      loading = false;
    }
  }

  $: centerNode = graph && center
    ? (graph.nodes || []).find(n => n.id === center)
    : null;
  $: title = center
    ? (centerNode ? centerNode.title : 'Record')
    : 'Catalog mindmap';

  // ── Graph construction ──────────────────────────────────────────────────────

  function buildGraph(g) {
    const ns = (g.nodes || []).map(n => ({
      ...n,
      x: 0, y: 0, vx: 0, vy: 0,
      isCenter: !!center && n.id === center,
    }));
    idIndex = new Map(ns.map((n, i) => [n.id, i]));

    const es = [];
    for (const e of (g.edges || [])) {
      const a = idIndex.get(e.src), b = idIndex.get(e.dst);
      if (a === undefined || b === undefined) continue;
      es.push({ a, b, rel: e.rel });
    }

    // Seed positions: center (or highest-authority node) in the middle, the
    // rest on a ring, so the sim untangles from a sane starting point.
    const cx = width / 2, cy = height / 2;
    let anchor = ns.findIndex(n => n.isCenter);
    if (anchor === -1 && ns.length) {
      anchor = ns.reduce((best, n, i) => (n.authority > (ns[best]?.authority ?? -1) ? i : best), 0);
    }
    ns.forEach((n, i) => {
      if (i === anchor) { n.x = cx; n.y = cy; return; }
      const a = (i / Math.max(1, ns.length)) * Math.PI * 2;
      const ring = 140 + (n.dangling ? 70 : 0);
      n.x = cx + Math.cos(a) * ring + (Math.random() - .5) * 40;
      n.y = cy + Math.sin(a) * ring + (Math.random() - .5) * 40;
    });

    nodes = ns;
    edges = es;
    alpha = 1;
  }

  function radius(n) {
    if (n.dangling) return 5;
    return (n.isCenter ? 10 : 6) + Math.min(n.authority || 0, 5) * 1.1;
  }

  // ── Force simulation ─────────────────────────────────────────────────────────

  function tick() {
    if (!nodes.length) return;
    alpha = Math.max(ALPHA_MIN, alpha - ALPHA_DECAY);
    const a = alpha;
    const idealDist = 120;
    const cx = width / 2, cy = height / 2;

    const fx = new Float64Array(nodes.length);
    const fy = new Float64Array(nodes.length);

    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const dx = nodes[i].x - nodes[j].x || 0.1;
        const dy = nodes[i].y - nodes[j].y || 0.1;
        const d = Math.sqrt(dx * dx + dy * dy) || 1;
        const f = (idealDist * idealDist) / (d * d) * a;
        const ux = dx / d, uy = dy / d;
        fx[i] += ux * f; fy[i] += uy * f;
        fx[j] -= ux * f; fy[j] -= uy * f;
      }
    }

    for (const { a: ai, b: bi } of edges) {
      const dx = nodes[bi].x - nodes[ai].x;
      const dy = nodes[bi].y - nodes[ai].y;
      const d = Math.sqrt(dx * dx + dy * dy) || 1;
      const f = (d - idealDist) * 0.05 * a;
      const ux = dx / d, uy = dy / d;
      fx[ai] += ux * f; fy[ai] += uy * f;
      fx[bi] -= ux * f; fy[bi] -= uy * f;
    }

    for (let i = 0; i < nodes.length; i++) {
      // Pin the ego center; gravity for everyone else.
      if (nodes[i].isCenter) continue;
      fx[i] += (cx - nodes[i].x) * 0.03 * a;
      fy[i] += (cy - nodes[i].y) * 0.03 * a;
    }

    const maxF = 6;
    for (let i = 0; i < nodes.length; i++) {
      const n = nodes[i];
      if (dragging?.node === n) continue;
      if (n.isCenter) { n.x = cx; n.y = cy; n.vx = 0; n.vy = 0; continue; }
      const fm = Math.sqrt(fx[i] * fx[i] + fy[i] * fy[i]);
      if (fm > maxF) { fx[i] *= maxF / fm; fy[i] *= maxF / fm; }
      n.vx = (n.vx + fx[i]) * 0.6;
      n.vy = (n.vy + fy[i]) * 0.6;
      n.x += n.vx;
      n.y += n.vy;
    }
  }

  // ── Rendering ────────────────────────────────────────────────────────────────

  function themeColor(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function draw() {
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    const W = width, H = height;
    const bg = themeColor('--bg');
    const accent = themeColor('--accent');
    const accentHover = themeColor('--accent-hover');
    const stamp = themeColor('--stamp');
    const warning = themeColor('--warning');
    const superseded = themeColor('--text-3');
    const textRgb = themeColor('--text');
    const REL = relStyles(accent, stamp);

    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, W, H);

    ctx.save();
    ctx.translate(pan.x + W / 2, pan.y + H / 2);
    ctx.scale(scale, scale);
    ctx.translate(-W / 2, -H / 2);

    // Edges
    for (const { a: ai, b: bi, rel } of edges) {
      const a = nodes[ai], b = nodes[bi];
      const st = REL[rel] || REL['relates'];
      ctx.beginPath();
      ctx.moveTo(a.x, a.y);
      ctx.lineTo(b.x, b.y);
      ctx.strokeStyle = st.color;
      ctx.lineWidth = (st.directed ? 1.4 : 1) / scale;
      if (st.dashed) ctx.setLineDash([5 / scale, 4 / scale]); else ctx.setLineDash([]);
      ctx.stroke();
      ctx.setLineDash([]);
      if (st.directed) drawArrow(ctx, a, b, st.color);
    }

    // Nodes
    for (const node of nodes) {
      const isHover = hovered === node;
      const isSel = selected === node;
      const r = radius(node);

      if (node.isCenter || isSel || isHover) {
        const glowR = r * 3;
        const grad = ctx.createRadialGradient(node.x, node.y, 0, node.x, node.y, glowR);
        grad.addColorStop(0, node.isCenter ? `color-mix(in srgb, ${stamp} 20%, transparent)` : `color-mix(in srgb, ${accent} 16%, transparent)`);
        grad.addColorStop(1, 'transparent');
        ctx.beginPath();
        ctx.arc(node.x, node.y, glowR, 0, Math.PI * 2);
        ctx.fillStyle = grad;
        ctx.fill();
      }

      ctx.beginPath();
      ctx.arc(node.x, node.y, r, 0, Math.PI * 2);
      if (node.dangling) {
        // Hollow, dashed ghost for a not-yet-written target.
        ctx.fillStyle = bg;
        ctx.fill();
        ctx.setLineDash([3 / scale, 3 / scale]);
        ctx.lineWidth = 1.4 / scale;
        ctx.strokeStyle = warning;
        ctx.stroke();
        ctx.setLineDash([]);
      } else {
        if (node.isCenter)      ctx.fillStyle = stamp;
        else if (node.superseded) ctx.fillStyle = superseded;
        else                    ctx.fillStyle = isHover || isSel ? accentHover : accent;
        ctx.fill();
        ctx.lineWidth = 1.5 / scale;
        ctx.strokeStyle = bg;
        ctx.stroke();
      }

      // Label
      const labelScale = Math.max(0.6, Math.min(1, scale));
      const fontSize = (node.isCenter ? 12 : 10.5) / labelScale;
      ctx.font = `${node.isCenter ? 500 : 400} ${fontSize}px 'IBM Plex Mono', ui-monospace, monospace`;
      ctx.textAlign = 'center';
      const show = isHover || isSel || node.isCenter || scale > 0.85;
      const labelAlpha = show ? (node.dangling ? 0.7 : 0.9) : 0;
      if (labelAlpha > 0) {
        const label = node.title.length > 28 ? node.title.slice(0, 27) + '…' : node.title;
        ctx.globalAlpha = labelAlpha;
        ctx.fillStyle = node.dangling ? warning : textRgb;
        if (node.dangling) ctx.font = `italic ${fontSize}px 'IBM Plex Mono', ui-monospace, monospace`;
        ctx.fillText(label, node.x, node.y + r + fontSize + 3);
        ctx.globalAlpha = 1;
      }
    }

    ctx.restore();
  }

  function drawArrow(ctx, a, b, color) {
    const r = radius(b);
    const dx = b.x - a.x, dy = b.y - a.y;
    const d = Math.sqrt(dx * dx + dy * dy) || 1;
    const ux = dx / d, uy = dy / d;
    // Tip sits just outside the target node.
    const tx = b.x - ux * (r + 1.5), ty = b.y - uy * (r + 1.5);
    const size = 6 / scale;
    const ang = Math.atan2(uy, ux);
    ctx.beginPath();
    ctx.moveTo(tx, ty);
    ctx.lineTo(tx - size * Math.cos(ang - 0.4), ty - size * Math.sin(ang - 0.4));
    ctx.lineTo(tx - size * Math.cos(ang + 0.4), ty - size * Math.sin(ang + 0.4));
    ctx.closePath();
    ctx.fillStyle = color;
    ctx.fill();
  }

  function loop() {
    tick();
    draw();
    animId = requestAnimationFrame(loop);
  }

  // ── Coordinate helpers ───────────────────────────────────────────────────────

  function toWorld(sx, sy) {
    return {
      x: (sx - pan.x - width / 2) / scale + width / 2,
      y: (sy - pan.y - height / 2) / scale + height / 2,
    };
  }

  function nodeAt(wx, wy) {
    for (let i = nodes.length - 1; i >= 0; i--) {
      const n = nodes[i];
      const r = (radius(n) + 5) / scale;
      const dx = n.x - wx, dy = n.y - wy;
      if (dx * dx + dy * dy < r * r) return n;
    }
    return null;
  }

  // ── Mouse events ─────────────────────────────────────────────────────────────

  function onMouseDown(e) {
    const rect = canvas.getBoundingClientRect();
    const { x: wx, y: wy } = toWorld(e.clientX - rect.left, e.clientY - rect.top);
    const node = nodeAt(wx, wy);
    if (node) {
      dragging = { node, ox: wx - node.x, oy: wy - node.y, moved: false };
      alpha = Math.max(alpha, 0.4);
    } else {
      const cx = e.clientX - rect.left, cy = e.clientY - rect.top;
      panning = { sx: cx, sy: cy, px: pan.x, py: pan.y };
    }
  }

  function onMouseMove(e) {
    const rect = canvas.getBoundingClientRect();
    const cx = e.clientX - rect.left, cy = e.clientY - rect.top;
    const { x: wx, y: wy } = toWorld(cx, cy);
    if (dragging) {
      dragging.node.x = wx - dragging.ox;
      dragging.node.y = wy - dragging.oy;
      dragging.node.vx = 0; dragging.node.vy = 0;
      dragging.moved = true;
    } else if (panning) {
      pan = { x: panning.px + (cx - panning.sx), y: panning.py + (cy - panning.sy) };
    } else {
      const prev = hovered;
      hovered = nodeAt(wx, wy);
      if (prev !== hovered) draw();
    }
  }

  function onMouseUp() {
    if (dragging && !dragging.moved) selected = dragging.node;
    dragging = null;
    panning = null;
  }

  function onWheel(e) {
    e.preventDefault();
    const rect = canvas.getBoundingClientRect();
    const cx = e.clientX - rect.left, cy = e.clientY - rect.top;
    const delta = e.deltaY > 0 ? 0.88 : 1.13;
    const newScale = Math.max(0.15, Math.min(5, scale * delta));
    const r = newScale / scale;
    pan = {
      x: cx - width / 2 - (cx - pan.x - width / 2) * r,
      y: cy - height / 2 - (cy - pan.y - height / 2) * r,
    };
    scale = newScale;
  }

  // ── Navigation from the selection card ───────────────────────────────────────

  function openRecord(n) { window.location.hash = `/memory/${n.id}`; }
  function centerOn(n) {
    selected = null;
    window.location.hash = `/mindmap/${n.id}`;
  }
  function viewDangling() { window.location.hash = '/links'; }

  function setDepth(d) {
    depth = Math.max(1, Math.min(4, d));
    if (center) load();
  }

  // ── Save / export ────────────────────────────────────────────────────────────

  // Build a markdown outline of the current view, linking every real node back
  // into the graph so the snapshot is itself a connected record.
  function buildOutline() {
    const real = (graph.nodes || []).filter(n => !n.dangling);
    const ghosts = (graph.nodes || []).filter(n => n.dangling);
    const byId = new Map((graph.nodes || []).map(n => [n.id, n]));
    const name = center ? (centerNode ? centerNode.title : 'record') : 'the catalog';

    let md = `Snapshot of the link graph around **${name}**, generated ${new Date().toISOString().slice(0, 10)}.\n\n`;
    md += `## Records (${real.length})\n\n`;
    for (const n of real) {
      md += `- [[${n.title}]]${n.taxonomy ? ` — \`${n.taxonomy}\`` : ''}\n`;
    }
    if (ghosts.length) {
      md += `\n## Missing targets (${ghosts.length})\n\n`;
      for (const n of ghosts) md += `- [[${n.title}]] _(dangling)_\n`;
    }
    if (graph.edges && graph.edges.length) {
      md += `\n## Connections (${graph.edges.length})\n\n`;
      for (const e of graph.edges) {
        const s = byId.get(e.src), d = byId.get(e.dst);
        if (!s || !d) continue;
        md += `- [[${s.title}]] → _${e.rel}_ → [[${d.title}]]\n`;
      }
    }
    return md;
  }

  async function saveMindmap() {
    if (!graph) return;
    saving = true; saveMsg = '';
    try {
      const name = center ? (centerNode ? centerNode.title : 'record') : 'catalog';
      const stamp = new Date().toISOString().slice(0, 10);
      const created = await apiCreateMemory({
        title: `Mindmap — ${name} (${stamp})`,
        taxonomy: 'derived.mindmaps',
        content: buildOutline(),
      });
      saveMsg = 'Saved';
      setTimeout(() => { window.location.hash = `/memory/${created.id}`; }, 500);
    } catch (e) {
      saveMsg = (e && (e.error || e.message)) || 'Save failed';
    } finally {
      saving = false;
    }
  }

  function download(filename, blob) {
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url; a.download = filename;
    document.body.appendChild(a); a.click(); a.remove();
    URL.revokeObjectURL(url);
  }

  function exportMarkdown() {
    if (!graph) return;
    const name = center ? (centerNode ? centerNode.title : 'record') : 'catalog';
    download(`mindmap-${slug(name)}.md`, new Blob([`# Mindmap — ${name}\n\n${buildOutline()}`], { type: 'text/markdown' }));
  }

  function exportPNG() {
    if (!canvas) return;
    const name = center ? (centerNode ? centerNode.title : 'record') : 'catalog';
    canvas.toBlob(b => { if (b) download(`mindmap-${slug(name)}.png`, b); }, 'image/png');
  }

  function slug(s) {
    return (s || 'mindmap').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40) || 'mindmap';
  }

  // ── Lifecycle ─────────────────────────────────────────────────────────────────

  const HEADER_H = 48;
  function applySize() {
    width = window.innerWidth;
    height = window.innerHeight - HEADER_H;
    if (canvas) { canvas.width = width; canvas.height = height; }
  }

  function goBack() { window.location.hash = center ? `/memory/${center}` : '/'; }

  onMount(() => {
    applySize();
    load();
    window.addEventListener('resize', applySize);
    canvas.addEventListener('wheel', onWheel, { passive: false });
    animId = requestAnimationFrame(loop);
    return () => {
      window.removeEventListener('resize', applySize);
      canvas.removeEventListener('wheel', onWheel);
      cancelAnimationFrame(animId);
    };
  });

  onDestroy(() => cancelAnimationFrame(animId));
</script>

<div class="overlay">
  <div class="header">
    <button class="back-btn" on:click={goBack}>
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 3L5 8l5 5"/></svg>
      {center ? 'Record' : 'Catalog'}
    </button>

    <span class="title">{title}</span>
    {#if center}<span class="badge">ego · {depth} hop{depth > 1 ? 's' : ''}</span>
    {:else}<span class="badge">corpus</span>{/if}

    {#if center}
      <div class="depth">
        <button class="depth-btn" on:click={() => setDepth(depth - 1)} disabled={depth <= 1} title="Fewer hops">–</button>
        <button class="depth-btn" on:click={() => setDepth(depth + 1)} disabled={depth >= 4} title="More hops">+</button>
      </div>
    {/if}

    <div class="actions">
      {#if $canWrite}
        <button class="act" on:click={saveMindmap} disabled={saving || loading || !graph}>
          {saving ? 'Saving…' : saveMsg || 'Save as record'}
        </button>
      {/if}
      <button class="act" on:click={exportMarkdown} disabled={loading || !graph}>.md</button>
      <button class="act" on:click={exportPNG} disabled={loading || !graph}>.png</button>
    </div>
  </div>

  {#if loading}
    <div class="state"><div class="spinner"></div></div>
  {:else if error}
    <div class="state"><p class="state__err">{error}</p><button class="act" on:click={load}>Retry</button></div>
  {:else if graph && graph.nodes.length === 0}
    <div class="state">
      <p class="state__empty">
        {#if center}This record has no links yet. Reference other records with <code>[[Title]]</code> to grow its map.
        {:else}No links anywhere yet. Connect records with <code>[[Title]]</code> wiki-links and they'll appear here.{/if}
      </p>
    </div>
  {/if}

  <canvas
    bind:this={canvas}
    class:hidden={loading || error}
    on:mousedown={onMouseDown}
    on:mousemove={onMouseMove}
    on:mouseup={onMouseUp}
    on:mouseleave={() => { hovered = null; panning = null; dragging = null; }}
    style:cursor={dragging ? 'grabbing' : hovered ? 'pointer' : panning ? 'grabbing' : 'grab'}
  />

  <!-- Legend -->
  {#if !loading && !error && graph && graph.nodes.length}
    <div class="legend">
      <span class="lg"><i class="ln ln--relates"></i>relates</span>
      <span class="lg"><i class="ln ln--depends"></i>depends-on</span>
      <span class="lg"><i class="ln ln--supersedes"></i>supersedes</span>
      <span class="lg"><i class="ln ln--contradicts"></i>contradicts</span>
      <span class="lg"><i class="dot dot--ghost"></i>missing target</span>
    </div>
    <div class="hint">Scroll to zoom · drag to pan · click a node</div>
  {/if}

  <!-- Selection card -->
  {#if selected}
    <div class="card">
      <button class="card__close" on:click={() => selected = null} aria-label="Close">×</button>
      <div class="card__title" class:card__title--ghost={selected.dangling}>{selected.title}</div>
      {#if selected.dangling}
        <div class="card__meta">Unresolved link target — no record yet.</div>
        <div class="card__actions">
          <button class="card__btn" on:click={viewDangling}>View in dangling links →</button>
        </div>
      {:else}
        {#if selected.taxonomy}<code class="card__tax">{selected.taxonomy}</code>{/if}
        <div class="card__meta">
          {selected.authority} inbound link{selected.authority === 1 ? '' : 's'}{selected.superseded ? ' · superseded' : ''}
        </div>
        <div class="card__actions">
          <button class="card__btn" on:click={() => openRecord(selected)}>Open record →</button>
          {#if !selected.isCenter}<button class="card__btn card__btn--ghost" on:click={() => centerOn(selected)}>Center here</button>{/if}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .overlay { position: fixed; inset: 0; z-index: 50; background: var(--bg); display: flex; flex-direction: column; }

  .header {
    display: flex; align-items: center; gap: 12px; padding: 0 18px; height: 48px;
    border-bottom: 1px solid var(--border-mid); background: var(--bg); flex-shrink: 0; z-index: 2;
  }
  .back-btn {
    display: flex; align-items: center; gap: 5px; padding: 5px 12px; background: transparent;
    border: 1px solid var(--border-mid); border-radius: var(--radius); color: var(--text-2);
    font-family: var(--font); font-size: 13px; cursor: pointer;
    transition: color .15s, border-color .15s, background .15s; flex-shrink: 0;
  }
  .back-btn:hover { color: var(--text); border-color: var(--text-3); background: var(--bg-hover); }
  .back-btn svg { width: 12px; height: 12px; }

  .title {
    font-family: var(--font-display); font-size: 17px; font-weight: 540; letter-spacing: -.01em;
    color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 40vw;
  }
  .badge {
    font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .08em; text-transform: uppercase;
    color: var(--text-3); border: 1px solid var(--border); border-radius: 99px; padding: 2px 8px; flex-shrink: 0;
  }

  .depth { display: flex; gap: 2px; background: var(--bg-subtle); border: 1px solid var(--border); border-radius: var(--radius); padding: 2px; }
  .depth-btn {
    width: 22px; height: 22px; border: none; background: transparent; border-radius: 4px;
    color: var(--text-2); font-size: 15px; line-height: 1; cursor: pointer;
  }
  .depth-btn:hover:not(:disabled) { background: var(--bg-hover); color: var(--text); }
  .depth-btn:disabled { opacity: .35; cursor: default; }

  .actions { margin-left: auto; display: flex; gap: 6px; flex-shrink: 0; }
  .act {
    padding: 5px 11px; background: transparent; border: 1px solid var(--border-mid); border-radius: var(--radius);
    color: var(--text-2); font-family: var(--font-mono); font-size: 11.5px; cursor: pointer;
    transition: color .15s, border-color .15s, background .15s;
  }
  .act:hover:not(:disabled) { color: var(--text); border-color: var(--accent); background: var(--bg-hover); }
  .act:disabled { opacity: .45; cursor: default; }

  .state {
    position: absolute; inset: 48px 0 0 0; display: flex; flex-direction: column;
    align-items: center; justify-content: center; gap: 14px; padding: 0 24px; text-align: center; z-index: 1;
  }
  .state__err { color: var(--stamp, #b4503c); font-size: 14px; }
  .state__empty { color: var(--text-2); font-size: 15px; line-height: 1.6; max-width: 46ch; }
  .state code, .state__empty code {
    font-family: var(--font-mono); font-size: .9em; background: var(--bg-subtle);
    border: 1px solid var(--border); border-radius: 3px; padding: 0 4px; color: var(--accent);
  }
  .spinner { width: 28px; height: 28px; border: 2.5px solid var(--border-mid); border-top-color: var(--accent); border-radius: 50%; animation: spin .7s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }

  canvas { position: absolute; top: 48px; left: 0; display: block; }
  canvas.hidden { visibility: hidden; }

  .legend {
    position: absolute; left: 16px; bottom: 14px; z-index: 3;
    display: flex; flex-wrap: wrap; gap: 10px 14px; max-width: 60vw;
    background: color-mix(in srgb, var(--bg) 90%, transparent); border: 1px solid var(--border); border-radius: var(--radius-md);
    padding: 8px 12px; backdrop-filter: blur(2px);
  }
  .lg { display: inline-flex; align-items: center; gap: 6px; font-family: var(--font-mono); font-size: 10.5px; color: var(--text-2); }
  .ln { width: 16px; height: 0; border-top-width: 2px; border-top-style: solid; display: inline-block; }
  .ln--relates { border-top-color: color-mix(in srgb, var(--accent) 40%, transparent); }
  .ln--depends { border-top-color: color-mix(in srgb, var(--accent) 70%, transparent); }
  .ln--supersedes { border-top-style: dashed; border-top-color: color-mix(in srgb, var(--stamp) 70%, transparent); }
  .ln--contradicts { border-top-color: color-mix(in srgb, var(--stamp) 75%, transparent); }
  .dot { width: 9px; height: 9px; border-radius: 50%; display: inline-block; }
  .dot--ghost { background: transparent; border: 1.4px dashed var(--warning); }

  .hint {
    position: absolute; right: 16px; bottom: 16px; z-index: 3;
    font-family: var(--font-mono); font-size: 10px; letter-spacing: .03em; color: var(--text-3);
  }

  .card {
    position: absolute; top: 62px; right: 16px; z-index: 4; width: 260px;
    background: var(--bg); border: 1px solid var(--border-mid); border-radius: var(--radius-md);
    padding: 14px 16px; box-shadow: var(--shadow-md);
  }
  .card__close {
    position: absolute; top: 8px; right: 10px; border: none; background: transparent;
    font-size: 18px; line-height: 1; color: var(--text-3); cursor: pointer;
  }
  .card__close:hover { color: var(--text); }
  .card__title { font-family: var(--font-display); font-size: 16px; font-weight: 540; color: var(--text); margin: 0 18px 8px 0; line-height: 1.25; }
  .card__title--ghost { font-style: italic; color: var(--warning); }
  .card__tax {
    display: inline-block; font-family: var(--font-mono); font-size: 11px; color: var(--accent);
    background: var(--bg-subtle); border: 1px solid var(--border); border-radius: var(--radius); padding: 1px 7px; margin-bottom: 8px;
  }
  .card__meta { font-family: var(--font-mono); font-size: 10.5px; color: var(--text-3); margin-bottom: 12px; }
  .card__actions { display: flex; flex-direction: column; gap: 6px; }
  .card__btn {
    padding: 6px 10px; background: var(--accent); border: none; border-radius: var(--radius);
    color: var(--accent-text); font-family: var(--font); font-size: 12.5px; cursor: pointer; text-align: center;
    transition: opacity .15s;
  }
  .card__btn:hover { opacity: .88; }
  .card__btn--ghost { background: transparent; border: 1px solid var(--border-mid); color: var(--text-2); }
  .card__btn--ghost:hover { color: var(--text); border-color: var(--accent); opacity: 1; }
</style>
