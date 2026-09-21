<script>
  import { onMount } from 'svelte';
  import { theme, THEMES } from '../lib/store.js';

  let open = false;

  // $theme is 'system' | one of THEMES' ids. In 'system' mode the trigger's
  // own swatch should still show what the reader is actually looking at, so
  // track the OS preference the same way global.css's media query does.
  let prefersDark = typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: dark)').matches;
  onMount(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = (e) => { prefersDark = e.matches; };
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  });
  $: activeId = $theme !== 'system' ? $theme : (prefersDark ? 'dark' : 'light');
  $: active = THEMES.find((t) => t.id === activeId) || THEMES[0];

  function pick(id) {
    theme.set(id);
    open = false;
  }
</script>

<svelte:window on:keydown={(e) => e.key === 'Escape' && (open = false)} />

<div class="picker">
  {#if open}
    <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
    <div class="scrim" on:click={() => (open = false)}></div>
    <div class="menu" role="menu">
      <div class="menu__label">Theme</div>
      <button class="row" class:active={$theme === 'system'} on:click={() => pick('system')}>
        <span class="swatch swatch--system"></span>
        <span class="row__label">System</span>
        {#if $theme === 'system'}
          <svg class="check" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M2.5 7.5l3 3 6-7"/></svg>
        {/if}
      </button>
      {#each THEMES as t (t.id)}
        <button class="row" class:active={$theme === t.id} on:click={() => pick(t.id)}>
          <span class="swatch" style="background: linear-gradient(135deg, {t.swatch} 55%, {t.accent} 55%)"></span>
          <span class="row__label">{t.label}</span>
          {#if $theme === t.id}
            <svg class="check" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M2.5 7.5l3 3 6-7"/></svg>
          {/if}
        </button>
      {/each}
    </div>
  {/if}

  <button
    class="trigger"
    on:click={() => (open = !open)}
    aria-haspopup="menu"
    aria-expanded={open}
    title="Theme: {$theme === 'system' ? 'System' : active.label}"
  >
    <span class="swatch swatch--trigger" style="background: linear-gradient(135deg, {active.swatch} 55%, {active.accent} 55%)"></span>
  </button>
</div>

<style>
  .picker { position: relative; }

  .trigger {
    display: flex; align-items: center; justify-content: center;
    width: 22px; height: 22px; flex-shrink: 0; padding: 0;
    background: transparent; border: 1px solid transparent; border-radius: var(--radius);
    cursor: pointer;
    transition: background var(--transition), border-color var(--transition);
  }
  .trigger:hover { background: var(--sidebar-hover); border-color: var(--sidebar-border); }

  .swatch {
    display: block; width: 13px; height: 13px; flex-shrink: 0;
    border-radius: 50%; border: 1px solid var(--sidebar-border);
  }
  .swatch--system { background: linear-gradient(135deg, #f6f2e9 50%, #1c1a15 50%); }
  .swatch--trigger { width: 14px; height: 14px; }

  .scrim { position: fixed; inset: 0; z-index: 30; }

  .menu {
    position: absolute; right: 0; top: calc(100% + 6px); z-index: 31;
    width: 172px;
    background: var(--bg-raised);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-md);
    padding: 6px;
  }
  .menu__label {
    font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .12em; text-transform: uppercase;
    color: var(--text-3); padding: 4px 8px 5px;
  }

  .row {
    display: flex; align-items: center; gap: 9px; width: 100%;
    padding: 6px 8px; background: transparent; border: none; border-radius: var(--radius);
    cursor: pointer; text-align: left;
    font-family: var(--font); font-size: 13.5px; color: var(--text);
    transition: background var(--transition);
  }
  .row:hover { background: var(--bg-hover); }
  .row.active { background: var(--accent-wash); }
  .row__label { flex: 1; }
  .check { width: 13px; height: 13px; color: var(--accent); flex-shrink: 0; }
</style>
