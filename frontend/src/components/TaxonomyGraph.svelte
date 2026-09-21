<script>
  import { onMount, onDestroy } from 'svelte';
  import { catalog, taxonomyFilter, graphView } from '../lib/store.js';
  import { loadMemories } from '../lib/data.js';

  let canvas;
  let width = 0;
  let height = 0;
  let animId;

  // Simulation
  let nodes = [];
  let edges = [];
  let alpha = 1;
  const ALPHA_MIN = 0.015;
  const ALPHA_DECAY = 0.022;

  // View
  let pan = { x: 0, y: 0 };
  let scale = 1;

  // Interaction
  let dragging = null;  // { node, ox, oy }
  let panning = null;   // { sx, sy, px, py }
  let hovered = null;

  // ── Graph construction ────────────────────────────────────────────────────

  function buildGraph(entries) {
    const nodeMap = new Map();

    for (const entry of entries) {
      if (!nodeMap.has(entry.path)) {
        const parts = entry.path.split('.');
        nodeMap.set(entry.path, {
          id: entry.path,
          label: parts[parts.length - 1],
          isRoot: parts.length === 1,
          count: entry.count,
          x: 0, y: 0, vx: 0, vy: 0,
        });
      }
      const parts = entry.path.split('.');
      for (let i = 1; i < parts.length; i++) {
        const parentPath = parts.slice(0, i).join('.');
        if (!nodeMap.has(parentPath)) {
          nodeMap.set(parentPath, {
            id: parentPath,
            label: parts[i - 1],
            isRoot: i === 1,
            count: 0,
            x: 0, y: 0, vx: 0, vy: 0,
          });
        }
      }
    }

    const ns = Array.from(nodeMap.values());
    // Initialise: roots in inner ring, children in outer ring
    const roots = ns.filter(n => n.isRoot);
    const children = ns.filter(n => !n.isRoot);
    const cx = width / 2, cy = height / 2;
    roots.forEach((n, i) => {
      const a = (i / roots.length) * Math.PI * 2;
      n.x = cx + Math.cos(a) * 90 + (Math.random() - .5) * 30;
      n.y = cy + Math.sin(a) * 90 + (Math.random() - .5) * 30;
      n.vx = 0; n.vy = 0;
    });
    children.forEach((n, i) => {
      const a = (i / Math.max(1, children.length)) * Math.PI * 2;
      n.x = cx + Math.cos(a) * 200 + (Math.random() - .5) * 40;
      n.y = cy + Math.sin(a) * 200 + (Math.random() - .5) * 40;
      n.vx = 0; n.vy = 0;
    });

    const es = [];
    ns.forEach((node, i) => {
      const dot = node.id.lastIndexOf('.');
      if (dot !== -1) {
        const pid = node.id.slice(0, dot);
        const pi = ns.findIndex(n => n.id === pid);
        if (pi !== -1) es.push([pi, i]);
      }
    });

    nodes = ns;
    edges = es;
    alpha = 1;
  }

  // ── Force simulation ──────────────────────────────────────────────────────

  function tick() {
    if (!nodes.length) return;

    alpha = Math.max(ALPHA_MIN, alpha - ALPHA_DECAY);
    const a = alpha;
    const idealDist = 120;
    const cx = width / 2, cy = height / 2;

    // Accumulate forces into temp arrays
    const fx = new Float64Array(nodes.length);
    const fy = new Float64Array(nodes.length);

    // Repulsion (all pairs)
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

    // Spring attraction along edges
    for (const [ai, bi] of edges) {
      const dx = nodes[bi].x - nodes[ai].x;
      const dy = nodes[bi].y - nodes[ai].y;
      const d = Math.sqrt(dx * dx + dy * dy) || 1;
      const f = (d - idealDist) * 0.05 * a;
      const ux = dx / d, uy = dy / d;
      fx[ai] += ux * f; fy[ai] += uy * f;
      fx[bi] -= ux * f; fy[bi] -= uy * f;
    }

    // Centering gravity
    for (let i = 0; i < nodes.length; i++) {
      fx[i] += (cx - nodes[i].x) * 0.03 * a;
      fy[i] += (cy - nodes[i].y) * 0.03 * a;
    }

    // Clamp total force per node, then integrate
    const maxF = 6;
    for (let i = 0; i < nodes.length; i++) {
      const n = nodes[i];
      if (dragging?.node === n) continue;
      const fm = Math.sqrt(fx[i] * fx[i] + fy[i] * fy[i]);
      if (fm > maxF) { fx[i] *= maxF / fm; fy[i] *= maxF / fm; }
      n.vx = (n.vx + fx[i]) * 0.6;
      n.vy = (n.vy + fy[i]) * 0.6;
      n.x += n.vx;
      n.y += n.vy;
    }
  }

  // ── Rendering ─────────────────────────────────────────────────────────────

  // Canvas paints pixels, not CSS — it can't inherit custom properties, so the
  // palette is re-read from the live theme each frame. Cheap relative to the
  // force layout already running per frame, and it keeps the graph in step
  // the instant the reader flips light/dark.
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
    const textMuted = themeColor('--text-2');
    const textFaint = themeColor('--text-3');
    const textRgb = themeColor('--text');

    // Background — paper
    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, W, H);

    // Pan + zoom transform
    ctx.save();
    ctx.translate(pan.x + W / 2, pan.y + H / 2);
    ctx.scale(scale, scale);
    ctx.translate(-W / 2, -H / 2);

    // Edges
    for (const [ai, bi] of edges) {
      const a = nodes[ai], b = nodes[bi];
      ctx.beginPath();
      ctx.moveTo(a.x, a.y);
      ctx.lineTo(b.x, b.y);
      ctx.strokeStyle = `color-mix(in srgb, ${accent} 22%, transparent)`;
      ctx.lineWidth = 1 / scale;
      ctx.stroke();
    }

    // Nodes
    for (const node of nodes) {
      const isActive = $taxonomyFilter === node.id;
      const isHover = hovered === node;
      const r = node.isRoot ? 9 : 5.5;

      // Soft halo for active / hover
      if (isActive || isHover) {
        const glowR = r * 3;
        const grad = ctx.createRadialGradient(node.x, node.y, 0, node.x, node.y, glowR);
        grad.addColorStop(0, isActive ? `color-mix(in srgb, ${stamp} 20%, transparent)` : `color-mix(in srgb, ${accent} 16%, transparent)`);
        grad.addColorStop(1, 'transparent');
        ctx.beginPath();
        ctx.arc(node.x, node.y, glowR, 0, Math.PI * 2);
        ctx.fillStyle = grad;
        ctx.fill();
      }

      // Core dot — ink on paper
      ctx.beginPath();
      ctx.arc(node.x, node.y, r, 0, Math.PI * 2);
      if (isActive) {
        ctx.fillStyle = stamp;
      } else if (node.isRoot) {
        ctx.fillStyle = isHover ? accentHover : accent;
      } else {
        ctx.fillStyle = isHover ? textMuted : textFaint;
      }
      ctx.fill();

      // Paper ring to lift the dot off the lines
      ctx.lineWidth = 1.5 / scale;
      ctx.strokeStyle = bg;
      ctx.stroke();

      // Label
      const labelScale = Math.max(0.6, Math.min(1, scale));
      const fontSize = (node.isRoot ? 11.5 : 10.5) / labelScale;
      ctx.font = `${node.isRoot ? 500 : 400} ${fontSize}px 'IBM Plex Mono', ui-monospace, monospace`;
      ctx.textAlign = 'center';
      const labelAlpha = isHover || isActive ? 1 : node.isRoot ? 0.82 : (scale > 0.8 ? 0.55 : 0);
      ctx.globalAlpha = labelAlpha;
      ctx.fillStyle = textRgb;
      ctx.fillText(node.label, node.x, node.y + r + fontSize + 3);
      ctx.globalAlpha = 1;

      // Count badge (on hover/active)
      if (node.count > 0 && (isHover || isActive)) {
        ctx.font = `${9 / labelScale}px 'IBM Plex Mono', ui-monospace, monospace`;
        ctx.globalAlpha = 0.85;
        ctx.fillStyle = textMuted;
        ctx.fillText(node.count, node.x, node.y + r + fontSize * 2 + 5);
        ctx.globalAlpha = 1;
      }
    }

    ctx.restore();
  }

  function loop() {
    tick();
    draw();
    animId = requestAnimationFrame(loop);
  }

  // ── Coordinate helpers ────────────────────────────────────────────────────

  function toWorld(sx, sy) {
    return {
      x: (sx - pan.x - width / 2) / scale + width / 2,
      y: (sy - pan.y - height / 2) / scale + height / 2,
    };
  }

  function nodeAt(wx, wy) {
    const hitR = 1 / scale; // inverse scale to keep hit area consistent
    for (let i = nodes.length - 1; i >= 0; i--) {
      const n = nodes[i];
      const r = (n.isRoot ? 14 : 10) * hitR;
      const dx = n.x - wx, dy = n.y - wy;
      if (dx * dx + dy * dy < r * r) return n;
    }
    return null;
  }

  // ── Mouse events ──────────────────────────────────────────────────────────

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
      if (prev !== hovered) draw(); // immediate redraw on hover change
    }
  }

  function onMouseUp(e) {
    if (dragging && !dragging.moved) {
      selectNode(dragging.node);
    }
    dragging = null;
    panning = null;
  }

  function onWheel(e) {
    e.preventDefault();
    const rect = canvas.getBoundingClientRect();
    const cx = e.clientX - rect.left;
    const cy = e.clientY - rect.top;
    const delta = e.deltaY > 0 ? 0.88 : 1.13;
    const newScale = Math.max(0.15, Math.min(5, scale * delta));
    const r = newScale / scale;
    pan = {
      x: cx - width / 2 - (cx - pan.x - width / 2) * r,
      y: cy - height / 2 - (cy - pan.y - height / 2) * r,
    };
    scale = newScale;
  }

  function selectNode(node) {
    const isSame = $taxonomyFilter === node.id;
    taxonomyFilter.set(isSame ? '' : node.id);
    graphView.set(false);
    loadMemories();
  }

  // ── Lifecycle ─────────────────────────────────────────────────────────────

  const HEADER_H = 44;

  function applySize() {
    width  = window.innerWidth;
    height = window.innerHeight - HEADER_H;
    if (canvas) { canvas.width = width; canvas.height = height; }
  }

  $: if ($catalog.length && width > 0 && height > 0) buildGraph($catalog);

  onMount(() => {
    applySize();
    if ($catalog.length) buildGraph($catalog);

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
    <button class="back-btn" on:click={() => graphView.set(false)}>
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M10 3L5 8l5 5"/>
      </svg>
      Back
    </button>
    <span class="title">Classification map</span>
    <span class="hint">Scroll to zoom · Drag to pan · Click node to filter</span>
  </div>

  <canvas
    bind:this={canvas}
    on:mousedown={onMouseDown}
    on:mousemove={onMouseMove}
    on:mouseup={onMouseUp}
    on:mouseleave={() => { hovered = null; panning = null; dragging = null; }}
    style:cursor={dragging ? 'grabbing' : hovered ? 'pointer' : panning ? 'grabbing' : 'grab'}
  />
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 50;
    background: var(--bg);
    display: flex;
    flex-direction: column;
  }

  .header {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 0 18px;
    height: 48px;
    border-bottom: 1px solid var(--border-mid);
    background: var(--bg);
    flex-shrink: 0;
    z-index: 1;
  }

  .back-btn {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: 5px 12px;
    background: transparent;
    border: 1px solid var(--border-mid);
    border-radius: var(--radius);
    color: var(--text-2);
    font-family: var(--font);
    font-size: 13px;
    cursor: pointer;
    transition: color 0.15s, border-color 0.15s, background 0.15s;
  }
  .back-btn:hover { color: var(--text); border-color: var(--text-3); background: var(--bg-hover); }
  .back-btn svg { width: 12px; height: 12px; }

  .title {
    font-family: var(--font-display);
    font-size: 17px;
    font-weight: 540;
    letter-spacing: -.01em;
    color: var(--text);
  }

  .hint {
    font-family: var(--font-mono);
    font-size: 10.5px;
    letter-spacing: .04em;
    color: var(--text-3);
    margin-left: auto;
  }

  canvas {
    position: absolute;
    top: 48px;
    left: 0;
    display: block;
  }
</style>
