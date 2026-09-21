<script>
  import MarkdownPreview from './MarkdownPreview.svelte';
  // Canonical source stays docs/legal/*.md in the repo root — imported raw so
  // there's exactly one copy of the text, not a second one to drift out of sync.
  import termsRaw from '../../../docs/legal/terms-of-service.md?raw';
  import privacyRaw from '../../../docs/legal/privacy-policy.md?raw';

  export let doc = 'terms'; // 'terms' | 'privacy'

  $: content = doc === 'privacy' ? privacyRaw : termsRaw;
</script>


<div class="legal">
  <div class="legal__inner">
    <a class="legal__back" href="#/">&larr; Back to rekam</a>
    <div class="legal__doc">
      <MarkdownPreview {content} format="markdown" />
    </div>
  </div>
</div>

<style>
  .legal {
    min-height: 100vh;
    background: var(--bg);
    padding: 40px 20px 80px;
  }
  .legal__inner {
    max-width: 760px;
    margin: 0 auto;
  }
  .legal__back {
    display: inline-block;
    margin-bottom: 20px;
    font-size: 13px;
    color: var(--text-2);
    text-decoration: none;
  }
  .legal__back:hover { text-decoration: underline; }
  .legal__doc {
    background: var(--bg-raised);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius-md);
    padding: 32px 40px;
  }
</style>
