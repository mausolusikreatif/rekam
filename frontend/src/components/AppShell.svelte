<script>
  import { onMount } from 'svelte';
  import { loadMemories, loadCatalog } from '../lib/data.js';
  import { graphView } from '../lib/store.js';
  import Sidebar from './Sidebar.svelte';
  import Toolbar from './Toolbar.svelte';
  import MemoryTable from './MemoryTable.svelte';
  import TaxonomyGraph from './TaxonomyGraph.svelte';

  let sidebarOpen = false;

  async function loadData() {
    await Promise.all([loadMemories(), loadCatalog()]);
  }

  onMount(loadData);
</script>

<!-- Mobile sidebar backdrop -->
{#if sidebarOpen}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="sidebar-backdrop" on:click={() => sidebarOpen = false}></div>
{/if}

<div class="app">
  <div class="sidebar-wrap" class:open={sidebarOpen}>
    <Sidebar />
  </div>

  <div class="main">
    {#if $graphView}
      <TaxonomyGraph />
    {:else}
      <Toolbar
        on:toggleSidebar={() => sidebarOpen = !sidebarOpen}
        on:newMemory={() => window.location.hash = '/new'}
      />
      <MemoryTable />
    {/if}
  </div>
</div>

<style>
  .app { height: 100%; display: flex; overflow: hidden; }
  .sidebar-wrap { display: flex; flex-shrink: 0; }
  .main { flex: 1; display: flex; flex-direction: column; overflow: hidden; min-width: 0; }

  .sidebar-backdrop {
    display: none;
    position: fixed; inset: 0;
    background: var(--overlay);
    z-index: 10;
  }

  @media (max-width: 768px) {
    .sidebar-wrap {
      position: fixed; left: 0; top: 0; bottom: 0;
      z-index: 20;
      transform: translateX(-100%);
      transition: transform var(--transition);
    }
    .sidebar-wrap.open { transform: translateX(0); }
    .sidebar-backdrop { display: block; }
  }
</style>
