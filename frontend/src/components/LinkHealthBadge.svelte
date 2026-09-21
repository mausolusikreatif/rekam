<script>
  import { activeKey, memories } from '../lib/store.js';
  import { apiEdgeHealth } from '../lib/api.js';

  let health = null;
  let error = false;

  // Refetch on identity change and whenever the record set changes (a write/edit
  // can resolve or create dangling links). Cheap read; the full list lives on /links.
  async function load() {
    try {
      health = await apiEdgeHealth();
      error = false;
    } catch (_) {
      error = true;
    }
  }
  $: $activeKey, $memories, load();

  $: state = !health || health.total_edges === 0
    ? 'idle'
    : health.dangling > 0 ? 'warn' : 'ok';

  $: label = error
    ? 'unavailable'
    : !health || health.total_edges === 0
      ? 'no links yet'
      : health.dangling > 0
        ? `${health.dangling} dangling`
        : `${health.resolved} linked`;

  function open() { window.location.hash = '/links'; }
</script>

<button class="lh" on:click={open} title="View dangling links">
  <span class="lh__dot lh__dot--{state}"></span>
  <span class="lh__label">Links</span>
  <span class="lh__value">{label}</span>
</button>

<style>
  .lh {
    display: flex; align-items: center; gap: 7px;
    width: 100%; padding: 9px 14px;
    background: transparent; border: none; border-top: 1px solid var(--sidebar-border);
    cursor: pointer; text-align: left;
    transition: background var(--transition);
  }
  .lh:hover { background: var(--sidebar-hover); }

  .lh__dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; }
  .lh__dot--ok   { background: var(--success); box-shadow: 0 0 0 2px color-mix(in srgb, var(--success) 18%, transparent); }
  .lh__dot--warn { background: var(--warning); box-shadow: 0 0 0 2px color-mix(in srgb, var(--warning) 18%, transparent); }
  .lh__dot--idle { background: var(--text-3); box-shadow: 0 0 0 2px var(--border); }

  .lh__label {
    font-family: var(--font-mono); font-size: 10px;
    letter-spacing: .13em; text-transform: uppercase; color: var(--text-3);
  }
  .lh__value {
    margin-left: auto; flex-shrink: 0;
    font-family: var(--font-mono); font-size: 10.5px;
    color: var(--sidebar-text); font-variant-numeric: tabular-nums;
  }
</style>
