<script>
  import { onMount } from 'svelte';
  import { apiReviewDue, apiGradeReview } from '../lib/api.js';
  import { dueCount } from '../lib/store.js';
  import MarkdownPreview from './MarkdownPreview.svelte';

  let queue = [];
  let stats = null;
  let loading = true;
  let error = '';
  let revealed = false;
  let grading = false;
  let reviewed = 0;

  // Grade buttons map to SM-2 grades; "Again" (<3) is a lapse that recurs today.
  const GRADES = [
    { label: 'Again', grade: 1, key: '1', hint: 'forgot' },
    { label: 'Hard',  grade: 3, key: '2', hint: '' },
    { label: 'Good',  grade: 4, key: '3', hint: '' },
    { label: 'Easy',  grade: 5, key: '4', hint: '' },
  ];

  $: card = queue[0] || null;

  async function load() {
    loading = true; error = '';
    try {
      const data = await apiReviewDue();
      queue = data.cards || [];
      stats = data.stats || null;
      dueCount.set(queue.length);
      revealed = false;
    } catch (e) {
      error = (e && (e.error || e.message)) || 'Could not load review queue';
    } finally {
      loading = false;
    }
  }
  onMount(load);

  async function grade(g) {
    if (!card || grading) return;
    grading = true;
    const current = card;
    try {
      await apiGradeReview(current.id, g);
      reviewed += 1;
      // Drop from the front; a lapse (<3) comes back at the end of this session.
      queue = queue.slice(1);
      if (g < 3) queue = [...queue, current];
      dueCount.set(queue.length);
      revealed = false;
    } catch (e) {
      error = (e && (e.error || e.message)) || 'Could not save grade';
    } finally {
      grading = false;
    }
  }

  function onKeydown(e) {
    if (loading || error || !card) return;
    if (!revealed && (e.key === ' ' || e.key === 'Enter')) {
      e.preventDefault();
      revealed = true;
      return;
    }
    if (revealed) {
      const g = GRADES.find(x => x.key === e.key);
      if (g) { e.preventDefault(); grade(g.grade); }
    }
  }

  function goBack() { window.location.hash = '/'; }
  function openRecord(id) { window.location.hash = `/memory/${id}`; }
</script>

<svelte:window on:keydown={onKeydown} />

<div class="review-page">
  <header class="review-header">
    <button class="back-btn" on:click={goBack}>
      <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9 2L4 7l5 5"/></svg>
      Catalog
    </button>
    {#if !loading && !error && (queue.length || reviewed)}
      <span class="progress">{reviewed} reviewed · {queue.length} left</span>
    {/if}
  </header>

  <div class="review-body">
    {#if loading}
      <p class="state">Loading review queue…</p>
    {:else if error}
      <p class="state state--err">{error}</p>
      <button class="ghost" on:click={load}>Retry</button>
    {:else if !card}
      <div class="done">
        <div class="done__mark">✓</div>
        {#if reviewed > 0}
          <h1 class="done__title">All caught up</h1>
          <p class="done__text">You reviewed {reviewed} record{reviewed === 1 ? '' : 's'}. Come back when more are due.</p>
        {:else if stats && stats.total === 0}
          <h1 class="done__title">Nothing in your deck yet</h1>
          <p class="done__text">Open a record and choose <strong>Add to review</strong> to start building a spaced-repetition deck.</p>
        {:else}
          <h1 class="done__title">Nothing due right now</h1>
          <p class="done__text">{stats ? stats.total : 0} record{stats && stats.total === 1 ? '' : 's'} in your deck, none due. Check back later.</p>
        {/if}
      </div>
    {:else}
      <div class="eyebrow">{card.taxonomy || 'review'}</div>
      <h1 class="prompt">{card.title}</h1>

      {#if !revealed}
        <button class="reveal" on:click={() => revealed = true}>
          Show answer <span class="reveal__key">space</span>
        </button>
      {:else}
        <div class="answer">
          <MarkdownPreview content={card.content} format={card.format} />
        </div>
        <div class="grades">
          {#each GRADES as g}
            <button class="grade grade--{g.label.toLowerCase()}" on:click={() => grade(g.grade)} disabled={grading}>
              <span class="grade__label">{g.label}</span>
              <span class="grade__key">{g.key}</span>
            </button>
          {/each}
        </div>
        <button class="open-link" on:click={() => openRecord(card.id)}>Open full record →</button>
      {/if}
    {/if}
  </div>
</div>

<style>
  .review-page { height: 100%; display: flex; flex-direction: column; background: var(--bg); overflow: hidden; }
  .review-header {
    height: var(--toolbar-h); display: flex; align-items: center; gap: 10px;
    padding: 0 20px; border-bottom: 1px solid var(--border); flex-shrink: 0;
  }
  .back-btn {
    display: inline-flex; align-items: center; gap: 5px; padding: 4px 8px; background: transparent;
    border: none; border-radius: var(--radius); color: var(--text-2); font-family: var(--font); font-size: 13px; cursor: pointer;
    transition: color var(--transition), background var(--transition);
  }
  .back-btn svg { width: 12px; height: 12px; }
  .back-btn:hover { color: var(--text); background: var(--bg-hover); }
  .progress { margin-left: auto; font-family: var(--font-mono); font-size: 11px; color: var(--text-3); }

  .review-body {
    flex: 1; overflow-y: auto; display: flex; flex-direction: column; align-items: center;
    padding: 8vh 24px 60px; text-align: center;
  }
  .state { font-size: 14px; color: var(--text-3); font-style: italic; }
  .state--err { color: var(--stamp, #b4503c); font-style: normal; }
  .ghost {
    margin-top: 10px; padding: 5px 12px; background: transparent; border: 1px solid var(--border-mid);
    border-radius: var(--radius); color: var(--text-2); font-size: 13px; cursor: pointer;
  }

  .eyebrow {
    font-family: var(--font-mono); font-size: 10.5px; font-weight: 500; letter-spacing: .14em;
    text-transform: uppercase; color: var(--accent); margin-bottom: 14px;
  }
  .prompt {
    font-family: var(--font-display); font-size: 2.4em; font-weight: 520; line-height: 1.15;
    letter-spacing: -.02em; color: var(--text); margin: 0 0 28px; max-width: 20ch;
  }

  .reveal {
    display: inline-flex; align-items: center; gap: 10px; padding: 11px 22px;
    background: var(--accent); border: none; border-radius: var(--radius-md);
    color: var(--accent-text); font-family: var(--font); font-size: 15px; cursor: pointer; transition: opacity .15s;
  }
  .reveal:hover { opacity: .9; }
  .reveal__key {
    font-family: var(--font-mono); font-size: 10px; letter-spacing: .06em; text-transform: uppercase;
    border: 1px solid color-mix(in srgb, var(--accent-text) 40%, transparent); border-radius: 4px; padding: 1px 6px;
  }

  .answer {
    width: 100%; max-width: 640px; margin: 0 0 28px; text-align: left;
    border-top: 1px solid var(--border); padding-top: 24px;
  }

  .grades { display: flex; gap: 10px; flex-wrap: wrap; justify-content: center; }
  .grade {
    display: flex; flex-direction: column; align-items: center; gap: 4px; min-width: 92px;
    padding: 12px 16px; background: var(--bg-subtle); border: 1px solid var(--border-mid);
    border-radius: var(--radius-md); cursor: pointer; transition: border-color .15s, background .15s, transform .05s;
  }
  .grade:hover:not(:disabled) { background: var(--bg-hover); transform: translateY(-1px); }
  .grade:active:not(:disabled) { transform: translateY(0); }
  .grade:disabled { opacity: .5; cursor: default; }
  .grade__label { font-family: var(--font); font-size: 14px; font-weight: 540; color: var(--text); }
  .grade__key { font-family: var(--font-mono); font-size: 10px; color: var(--text-3); }
  .grade--again { border-bottom: 2px solid var(--stamp); }
  .grade--hard  { border-bottom: 2px solid var(--warning); }
  .grade--good  { border-bottom: 2px solid var(--success); }
  .grade--easy  { border-bottom: 2px solid var(--accent); }

  .open-link {
    margin-top: 22px; background: transparent; border: none; color: var(--text-3);
    font-family: var(--font-mono); font-size: 11px; cursor: pointer; transition: color .15s;
  }
  .open-link:hover { color: var(--accent); }

  .done { display: flex; flex-direction: column; align-items: center; gap: 12px; margin-top: 6vh; }
  .done__mark {
    width: 48px; height: 48px; border-radius: 50%; display: flex; align-items: center; justify-content: center;
    background: var(--state-resolved-bg); color: var(--success); font-size: 24px;
  }
  .done__title { font-family: var(--font-display); font-size: 1.9em; font-weight: 520; color: var(--text); margin: 4px 0 0; }
  .done__text { font-size: 15px; color: var(--text-2); max-width: 44ch; line-height: 1.6; margin: 0; }
</style>
