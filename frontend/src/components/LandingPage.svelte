<script>
  import { onMount } from 'svelte';
  import { hasControlPlane } from '../lib/edition.js';
  import { theme } from '../lib/store.js';
  import { MEDIA_BASE, appLink, DOCS_URL } from '../lib/marketing-env.js';

  // Every button on this page is asking to make an account, so they open the
  // gate on signup. The nav's "Log in" is a plain link to #/login.
  function openSignup() {
    // Self-hosted builds have no self-service signup — the operator creates
    // accounts — so every call to action opens the login form instead.
    window.location.href = appLink(hasControlPlane ? '/signup' : '/login');
  }

  // The media is a real recording of a real instance (tests/visual/marketing/
  // run.sh), and that instance is always in one specific theme — there's no
  // such thing as a theme-agnostic screen recording. So the pipeline shoots
  // one full pass per theme in THEMES and this page picks the set that
  // matches the visitor's own theme, the same way the app itself does: the
  // explicit choice if there is one, otherwise the OS (which only ever
  // resolves to light or dark — an explicit-only theme like malleable is
  // never picked automatically).
  let prefersDark = typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: dark)').matches;
  onMount(() => {
    mounted = true;
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = (e) => { prefersDark = e.matches; };
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  });
  // 'light' ships with no suffix (the bare default, same as in global.css);
  // every other theme is its own name, matching what run.sh writes.
  $: resolvedTheme = $theme === 'system' ? (prefersDark ? 'dark' : 'light') : $theme;
  $: suffix = resolvedTheme === 'light' ? '' : `-${resolvedTheme}`;
  $: heroPoster = `${MEDIA_BASE}/hero-poster${suffix}.webp`;
  $: heroWebm = `${MEDIA_BASE}/hero${suffix}.webm`;
  $: heroMp4 = `${MEDIA_BASE}/hero${suffix}.mp4`;
  $: recordImg = `${MEDIA_BASE}/record${suffix}.webp`;
  $: searchImg = `${MEDIA_BASE}/search${suffix}.webp`;
  $: mindmapImg = `${MEDIA_BASE}/mindmap${suffix}.webp`;
  $: walkthroughWebm = `${MEDIA_BASE}/walkthrough${suffix}.webm`;
  $: walkthroughMp4 = `${MEDIA_BASE}/walkthrough${suffix}.mp4`;

  // <source> children only get re-read on an explicit load() — changing their
  // src attribute alone does nothing once the browser has parsed them once.
  // Reload whichever video element exists whenever the resolved theme changes
  // after mount (not on the initial mount itself, which already picked the
  // right set), and resume playback on the hero if it was running.
  let mounted = false;
  let prevSuffix;
  let walkthroughVideo;
  let heroVideo;
  let heroPlaying = false;

  // heroPlaying tracks the button's label/icon, not our intent — it is set
  // from the video's own play/pause events (in the markup) so it stays right
  // even when the browser stops the clip on its own (tab backgrounded, bfcache
  // restore, power-saving mode), not only when toggleHero is clicked.
  function toggleHero() {
    if (!heroVideo) return;
    if (heroVideo.paused) heroVideo.play().catch(() => {});
    else heroVideo.pause();
  }

  // Reduced motion means the poster frame and nothing else, unless the
  // visitor presses play themselves.
  onMount(() => {
    if (!heroVideo) return;
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    heroVideo.play().catch(() => {
      // Autoplay refused (some mobile power-saving modes). The poster stands
      // in and the play control still works.
    });
  });

  $: if (mounted && suffix !== prevSuffix) {
    prevSuffix = suffix;
    if (heroVideo) {
      const wasPlaying = !heroVideo.paused;
      heroVideo.load();
      if (wasPlaying) heroVideo.play().catch(() => {});
    }
    if (walkthroughVideo) walkthroughVideo.load();
  }

  const entries = [
    {
      title: 'Typed memory records',
      path: 'records',
      desc: 'Markdown, code, tables, and Mermaid diagrams — structured enough that an agent quotes the record instead of paraphrasing what it half-remembers.',
    },
    {
      title: 'Wiki-links with a type',
      path: 'records.links',
      desc: 'Connect records by title and say how they relate: depends-on, supersedes, contradicts. Those edges also rank what search returns.',
    },
    {
      title: 'Taxonomy and full-text search',
      path: 'catalog',
      desc: 'Dot-notation paths like work.projects.rekam, plus SQLite full-text search. An agent can find a record by where it lives or by asking in plain language.',
    },
    {
      title: 'Link health',
      path: 'catalog.health',
      desc: 'Links pointing at records that no longer exist, and records that clearly belong together but were never connected. Both are listed for you to fix.',
    },
    {
      title: 'Spaced review',
      path: 'review',
      desc: 'Put a record in a review deck and it comes back around on a schedule, so a decision made in March is still reachable in September.',
    },
  ];

  // The six things "sovereign" has to mean here. Each is something the product
  // actually does (spec/export.md, spec/mcp.md, spec/editions.md,
  // spec/identity.md, spec/revisions.md), not a promise to take on faith. The
  // write-attribution point is deliberately absent from `entries` above so the
  // phrase "The AI decided this" is said once on the page.
  const guarantees = [
    {
      title: 'Take the whole catalog with you',
      path: 'export',
      desc: 'Every readable record downloads as Markdown with YAML front matter, filed the way it lives here. The archive opens without rekam and imports anywhere.',
    },
    {
      title: 'Any model, no migration',
      path: 'mcp',
      desc: 'An agent from any provider reads and writes the same records over MCP. Change providers and the catalog does not move, because it was never theirs.',
    },
    {
      title: 'Run the whole server yourself',
      path: 'editions',
      desc: 'The same code builds a single binary you run on your own machine. Self-host and the keys, the database, and the access list never leave.',
    },
    {
      title: 'Access is granted, not assumed',
      path: 'identity.grants',
      desc: 'API keys and OAuth grants carry read and write scope down to individual taxonomy branches. Nothing reaches a record its grant did not name.',
    },
    {
      title: 'Every write has an author',
      path: 'revisions',
      desc: 'Each write is attributed to an identity you issued, and kept as a revision you can restore. "The AI decided this" is never the answer.',
    },
    {
      title: 'Nothing disappears quietly',
      path: 'tombstones',
      desc: 'Deleting a record is terminal, but a tombstone stays behind naming its title, who deleted it, and when — a disappearance recorded, not silent.',
    },
  ];

  const steps = [
    {
      title: 'Point an agent at the MCP endpoint',
      desc: 'Claude, ChatGPT, or something you wrote yourself. One endpoint, one API key, no setup per vendor.',
    },
    {
      title: 'Let it write records',
      desc: 'Agents file typed records under a taxonomy path and link them as they go. The store is yours from the first write, whether you are working solo or with a team.',
    },
    {
      title: 'Change providers',
      desc: 'Add a second provider, or leave the first one behind. The catalog does not move and nothing resets to zero.',
    },
  ];

  // The three editions (spec/editions.md), as a visitor would choose between
  // them: who holds the server, who it is for, how you begin, what it costs.
  // Cost here is one line per edition, not the full breakdown — that's what
  // /pricing is for (frontend/src/components/PricingPage.svelte); this is the
  // number a visitor needs to keep comparing editions without leaving the page.
  const editions = [
    {
      name: 'solo',
      title: 'On your own machine',
      facts: [
        { label: 'Who runs it', value: 'You do.' },
        { label: "Who it's for", value: 'A single person — your own decisions, notes, and runbooks.' },
        { label: 'How you start', code: 'rekam install', value: ', one binary, nothing to maintain.' },
        { label: 'What it costs', value: 'Free, permanently — there is no meter on this one.' },
      ],
    },
    {
      name: 'team',
      title: 'On a server your team can reach',
      cta: 'login',
      facts: [
        { label: 'Who runs it', value: 'You do.' },
        { label: "Who it's for", value: 'A group sharing one corpus, with roles and per-branch access.' },
        { label: 'How you start', value: 'Sign in at rekam.net to get the team build, then run it on your own server.' },
        { label: 'What it costs', value: '$399/year, flat — however many people are on it.' },
      ],
    },
    {
      name: 'managed',
      title: 'On our service',
      cta: 'signup',
      facts: [
        { label: 'Who runs it', value: 'We do, at rekam.net.' },
        { label: "Who it's for", value: 'Solo or a team, with nothing to run yourself.' },
        { label: 'How you start', value: 'Create an account and point an agent at the MCP endpoint.' },
        { label: 'What it costs', value: 'Free for one seat, $9/month for five.' },
      ],
    },
  ];
</script>

<nav class="lp-nav">
  <div class="lp-nav__inner">
    <a class="lp-brand" href="/">
      <span class="lp-mark">R</span>
      <span class="lp-wordmark">rekam</span>
    </a>
    <div class="lp-nav__links">
      <a class="lp-nav__link lp-nav__link--anchor" href="#features">What it stores</a>
      <a class="lp-nav__link lp-nav__link--anchor" href="#how">How it works</a>
      <a class="lp-nav__link" href="/pricing">Pricing</a>
      <a class="lp-nav__link" href={appLink('/login')}>Log in</a>
      <button class="btn-primary lp-nav__cta" on:click={openSignup}>Start free</button>
    </div>
  </div>
</nav>

<main>
<header class="lp-hero">
  <div class="lp-hero__inner">
    <div class="lp-hero__argument">
      <h1 class="lp-hero__title">Your memory, not the model's.</h1>
      <p class="lp-hero__lead">
        rekam is the memory your agents read and write — held by you, not locked
        inside a model vendor. Solo or with a team, on your own machine or on our
        managed service, the catalog is yours: you grant the access, you keep the
        records, and you can take them somewhere else.
      </p>
      <div class="lp-hero__actions">
        <button class="btn-primary btn-lg" on:click={openSignup}>Start free</button>
        <a class="lp-textlink" href={DOCS_URL}>Read the docs</a>
      </div>
    </div>

    <!-- The app, running. Recorded against a real instance; see
         tests/visual/marketing/README.md. It runs to the right edge of the
         window rather than stopping at the container, so the hero reads as one
         composition instead of a headline with a picture parked under it.
         Autoplaying is deliberate — a still of a catalog does not show that
         following a link goes anywhere — but it is muted, silent-tracked, and
         stoppable: an autoplaying loop with no way to stop it fails WCAG 2.2.2,
         and it is rude besides. -->
    <figure class="lp-plate lp-plate--bleed">
      <div class="lp-plate__frame">
        <video
          class="lp-plate__media"
          bind:this={heroVideo}
          muted loop playsinline
          preload="metadata"
          width="1280" height="800"
          poster={heroPoster}
          on:play={() => (heroPlaying = true)}
          on:pause={() => (heroPlaying = false)}
          aria-label="Screen recording: searching the rekam catalog for the deploy runbook, opening it, and following one of its typed links to the release checklist it depends on.">
          <source src={heroWebm} type="video/webm" />
          <source src={heroMp4} type="video/mp4" />
        </video>
        <button
          type="button"
          class="lp-plate__control"
          aria-label={heroPlaying ? 'Pause the demo' : 'Play the demo'}
          on:click={toggleHero}>
          {#if heroPlaying}
            <svg viewBox="0 0 12 12" aria-hidden="true"><rect x="2" y="1.5" width="3" height="9" rx=".5"/><rect x="7" y="1.5" width="3" height="9" rx=".5"/></svg>
          {:else}
            <svg viewBox="0 0 12 12" aria-hidden="true"><path d="M3 1.5l7 4.5-7 4.5z"/></svg>
          {/if}
          <span>{heroPlaying ? 'Pause' : 'Play'}</span>
        </button>
      </div>
      <figcaption class="lp-plate__caption">
        Searching for a runbook, opening it, and following the link it depends
        on — in the app, not a picture of it.
      </figcaption>
    </figure>
  </div>
</header>

<!-- The claim the hero makes, unpacked. No media here: the point is the list
     itself, and six screenshots would only bury it. -->
<section id="sovereignty" class="lp-section">
  <div class="lp-section__inner lp-spread">
    <div>
      <h2 class="lp-section__title">Sovereignty, spelled out.</h2>
      <p class="lp-section__lead">
        "Sovereign memory" is an easy phrase to print. These are the six things
        it has to mean here — each one a thing you can do, not a promise you
        have to take on faith.
      </p>
    </div>
    <dl class="lp-guarantees">
      {#each guarantees as guarantee}
        <div class="lp-guarantee">
          <dt class="lp-guarantee__head">
            <span class="lp-guarantee__title">{guarantee.title}</span>
            <span class="lp-guarantee__path">{guarantee.path}</span>
          </dt>
          <dd class="lp-guarantee__desc">{guarantee.desc}</dd>
        </div>
      {/each}
    </dl>
  </div>
</section>

<!-- The record gets the widest measure on the page. It is the thing the whole
     product is about, and it is the only shot where the detail — the typed
     chips, the taxonomy path — is the argument. -->
<section class="lp-section lp-section--recessed">
  <div class="lp-section__inner">
    <div class="lp-lede">
      <h2 class="lp-section__title">A record, and everything it points at.</h2>
      <p class="lp-section__lead">
        Markdown goes in whole — code, tables, diagrams. What makes it a catalog
        rather than a folder of notes is the double-bracket link: write
        <code class="lp-code">[[depends-on::Release checklist]]</code> and the
        record carries a typed edge that both search ranking and the map read.
      </p>
    </div>
    <figure class="lp-plate lp-plate--still lp-plate--wide">
      <div class="lp-plate__frame">
        <img
          class="lp-plate__media"
          src={recordImg}
          width="2400" height="2700" loading="lazy" decoding="async"
          alt="A rekam record titled Deploy runbook, filed under work.runbooks. Its opening paragraph carries three link chips labelled DEPENDS-ON Release checklist, SUPERSEDES Blue-green cutover, and CONTRADICTS Hotfix policy, followed by a shell code block, a table of stages, owners and rollbacks, and a linked-from block listing the records that point back at it." />
      </div>
      <figcaption class="lp-plate__caption">
        One record as rekam stores it. Two of its links resolve; the third
        contradicts this one, and says so where you can see it.
      </figcaption>
    </figure>
  </div>
</section>

<section id="features" class="lp-section">
  <div class="lp-section__inner">
    <h2 class="lp-section__title">One catalog, and every model reads the same one.</h2>
    <p class="lp-section__lead">
      Not a vector store and not a chat history locked inside one
      subscription — a catalog of records you hold, that any agent from any
      provider can work in.
    </p>

    <dl class="lp-entries">
      {#each entries as entry}
        <div class="lp-entry">
          <dt class="lp-entry__head">
            <span class="lp-entry__title">{entry.title}</span>
            <span class="lp-entry__path">{entry.path}</span>
          </dt>
          <dd class="lp-entry__desc">{entry.desc}</dd>
        </div>
      {/each}
    </dl>
  </div>
</section>

<section class="lp-section lp-section--recessed">
  <div class="lp-section__inner lp-lede">
    <h2 class="lp-section__title">Ask for it in the words you'd use.</h2>
    <p class="lp-section__lead">
      Full-text search over every record, re-ranked by how much of the rest of
      the catalog leans on each result. A record four others depend on outranks
      one that nothing points at, which is usually what you meant.
    </p>
    <figure class="lp-plate lp-plate--still">
      <div class="lp-plate__frame">
        <img
          class="lp-plate__media"
          src={searchImg}
          width="2400" height="1500" loading="lazy" decoding="async"
          alt="The rekam catalog with the command palette open over it and the query 'release'. Six records are listed, each with its taxonomy path on the right and the matched words highlighted in an excerpt beneath the title." />
      </div>
      <figcaption class="lp-plate__caption">
        Searching for "release". Every match is shown in place, under the
        taxonomy path the record is filed at.
      </figcaption>
    </figure>
  </div>
</section>

<section class="lp-section">
  <div class="lp-section__inner lp-lede">
    <h2 class="lp-section__title">The links make a map you can walk.</h2>
    <p class="lp-section__lead">
      Plain where records merely relate, arrowed where one depends on another,
      dashed where one supersedes another, red where two disagree. A link to a
      record nobody has written yet is drawn as an outline, so a gap looks like
      a gap rather than like nothing at all.
    </p>
    <figure class="lp-plate lp-plate--still">
      <div class="lp-plate__frame">
        <img
          class="lp-plate__media"
          src={mindmapImg}
          width="2400" height="1500" loading="lazy" decoding="async"
          alt="A force-directed graph of a rekam corpus. Deploy runbook sits at the centre as the largest node, connected to Release checklist, Hotfix policy, Blue-green cutover and Storage layout. A dashed outline node labelled 'unwritten spec' marks a link with no record behind it. A legend along the bottom names each edge type." />
      </div>
      <figcaption class="lp-plate__caption">
        A whole corpus at once. "Deploy runbook" is the hub because everything
        else points at it; "unwritten spec" is an outline because nothing has
        been filed there yet.
      </figcaption>
    </figure>
  </div>
</section>

<section class="lp-section lp-section--recessed">
  <div class="lp-section__inner lp-lede">
    <h2 class="lp-section__title">The longer version.</h2>
    <p class="lp-section__lead">
      A minute through the catalog, a record and its links, the map they build,
      and what link health flags as broken.
    </p>
    <figure class="lp-plate lp-plate--walkthrough">
      <div class="lp-plate__frame">
        <!-- muted because the clips are encoded with no audio track at all
             (ffmpeg -an), not to silence something. -->
        <video
          class="lp-plate__media"
          bind:this={walkthroughVideo}
          controls muted preload="none"
          width="1280" height="800"
          poster={heroPoster}>
          <source src={walkthroughWebm} type="video/webm" />
          <source src={walkthroughMp4} type="video/mp4" />
        </video>
      </div>
      <figcaption class="lp-plate__caption">
        Nothing loads until you press play.
      </figcaption>
    </figure>
  </div>
</section>

<!-- The three editions, side by side. The hero and the sovereignty section
     already promise self-host and managed are both first-class; this is where
     a visitor picks which one they are. -->
<section id="run" class="lp-section">
  <div class="lp-section__inner">
    <h2 class="lp-section__title">Your machine, your team's server, or ours.</h2>
    <p class="lp-section__lead">
      All three editions are one codebase built three ways. The records are the
      same and yours in each; what changes is who runs the server, and how you
      get it.
    </p>
    <div class="lp-editions">
      {#each editions as edition}
        <div class="lp-edition">
          <span class="lp-edition__name">{edition.name}</span>
          <h3 class="lp-edition__title">{edition.title}</h3>
          <dl class="lp-edition__facts">
            {#each edition.facts as fact}
              <div class="lp-edition__fact">
                <dt class="lp-edition__label">{fact.label}</dt>
                <dd class="lp-edition__value">
                  {#if fact.code}<code class="lp-code">{fact.code}</code>{/if}{fact.value}
                </dd>
              </div>
            {/each}
          </dl>
          {#if edition.cta === 'signup'}
            <button class="btn-primary lp-edition__cta-btn" on:click={openSignup}>Start free</button>
          {:else if edition.cta === 'login'}
            <a class="lp-textlink lp-edition__cta-link" href={appLink('/login')}>Sign in to rekam.net</a>
          {/if}
        </div>
      {/each}
    </div>
    <p class="lp-editions__note">
      Self-hosting is documented start to finish —
      <a class="lp-textlink" href={DOCS_URL}>read the self-hosting docs</a>.
    </p>
  </div>
</section>

<section id="how" class="lp-section lp-section--recessed">
  <div class="lp-section__inner lp-spread">
    <h2 class="lp-section__title">Three steps, and the memory stops being the vendor's.</h2>
    <ol class="lp-steps">
      {#each steps as step, i}
        <li class="lp-step">
          <span class="lp-step__n" aria-hidden="true">{i + 1}</span>
          <div class="lp-step__body">
            <h3 class="lp-step__title">{step.title}</h3>
            <p class="lp-step__desc">{step.desc}</p>
          </div>
        </li>
      {/each}
    </ol>
  </div>
</section>

<section id="pricing" class="lp-section lp-close">
  <div class="lp-section__inner lp-spread">
    <div>
      <h2 class="lp-section__title">Priced by who runs the server.</h2>
      <p class="lp-section__lead">
        Self-host it and it's free, permanently — there's nothing to meter on a
        binary you run yourself. Hosting it here starts free too, and only
        charges once you need more than one seat.
      </p>
    </div>
    <div class="lp-close__offer">
      <ul class="lp-facts">
        <li class="lp-fact">Solo, self-hosted: free forever, no account.</li>
        <li class="lp-fact">Managed at rekam.net: free for one seat, $9/month for five.</li>
        <li class="lp-fact">Team, self-hosted: $399/year flat, however many people are on it.</li>
      </ul>
      <div class="lp-close__actions">
        <button class="btn-primary btn-lg" on:click={openSignup}>Start free</button>
        <a class="lp-textlink" href="/pricing">See full pricing</a>
      </div>
    </div>
  </div>
</section>
</main>

<footer class="lp-footer">
  <div class="lp-footer__inner">
    <div class="lp-footer__brand">
      <span class="lp-mark lp-mark--sm">R</span>
      <span class="lp-wordmark lp-wordmark--sm">rekam</span>
    </div>
    <nav class="lp-footer__links" aria-label="Footer">
      <a class="lp-footer__link" href={DOCS_URL}>Docs</a>
      <a class="lp-footer__link" href="/pricing">Pricing</a>
      <a class="lp-footer__link" href="/terms">Terms</a>
      <a class="lp-footer__link" href="/privacy">Privacy</a>
    </nav>
    <p class="lp-footer__copy">
      © {new Date().getFullYear()} rekam — memory your agents can keep.
    </p>
  </div>
</footer>

<style>
  /* ═══════════════════════════════════════════════════════════════════════
     Landing page.

     Structure rather than decoration: hairline rules divide the page, and
     exactly one surface (the hero record) is raised off the paper. Mono type
     appears only where the content is literally machine syntax — taxonomy
     paths and link types — never as a small-label face.
     ═══════════════════════════════════════════════════════════════════════ */

  @media (prefers-reduced-motion: no-preference) {
    :global(html) { scroll-behavior: smooth; }
  }

  /* ── Nav ──────────────────────────────────────────────────────────────── */
  .lp-nav {
    position: sticky;
    top: 0;
    z-index: 50;
    background: color-mix(in srgb, var(--bg) 88%, transparent);
    backdrop-filter: saturate(140%) blur(8px);
    border-bottom: 1px solid var(--border);
  }
  .lp-nav__inner {
    max-width: 1080px;
    margin: 0 auto;
    padding: 13px 24px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
  }
  .lp-nav__links {
    display: flex;
    align-items: center;
    gap: 22px;
  }
  .lp-nav__link {
    color: var(--text-2);
    text-decoration: none;
    font-size: 15px;
  }
  .lp-nav__link:hover { color: var(--text); }
  .lp-nav__cta { font-size: 14px; padding: 7px 14px; }

  .lp-brand {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    text-decoration: none;
    color: inherit;
  }
  .lp-mark {
    width: 30px; height: 30px;
    background: var(--accent);
    color: var(--accent-text);
    border-radius: var(--radius);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-display);
    font-weight: 600;
    font-size: 17px;
    flex-shrink: 0;
  }
  .lp-mark--sm { width: 24px; height: 24px; font-size: 13px; }
  .lp-wordmark {
    font-family: var(--font-display);
    font-size: 21px;
    font-weight: 560;
    letter-spacing: -.015em;
  }
  .lp-wordmark--sm { font-size: 17px; }

  /* ── Hero: the argument beside the app, which runs off the edge ───────── */
  .lp-hero {
    padding: 0 24px;
    border-bottom: 1px solid var(--border-mid);
    /* The clip's negative margin must never turn into a horizontal scrollbar. */
    overflow: hidden;
  }
  .lp-hero__inner {
    max-width: 1080px;
    margin: 0 auto;
    padding: 78px 0 84px;
    display: grid;
    grid-template-columns: minmax(0, 390px) minmax(0, 1fr);
    gap: 60px;
    align-items: center;
  }
  .lp-hero__title {
    font-family: var(--font-display);
    font-size: clamp(34px, 4.2vw, 47px);
    font-weight: 500;
    letter-spacing: -.026em;
    line-height: 1.06;
    margin: 0 0 20px;
    color: var(--text);
  }
  .lp-hero__lead {
    font-size: 17px;
    line-height: 1.6;
    color: var(--text-2);
    margin: 0 0 30px;
  }
  .lp-hero__actions {
    display: flex;
    align-items: center;
    gap: 24px;
    flex-wrap: wrap;
  }
  .lp-textlink {
    color: var(--accent);
    font-size: 16px;
    text-decoration: none;
    border-bottom: 1px solid color-mix(in srgb, var(--accent) 32%, transparent);
    padding-bottom: 1px;
  }
  .lp-textlink:hover { border-bottom-color: var(--accent); }

  /* ── Plates ───────────────────────────────────────────────────────────── */
  /*
     Every shot here is of the app, and the app's paper is this page's paper —
     the same --bg. Dropped straight onto the page a screenshot would have no
     edge at all, so each one is set like a plate in a printed book: a hairline
     rule around it, the recessed band behind it doing the separating, and a
     caption underneath saying what you are looking at.

     No drop shadow. A soft grey shadow under every image is the stock way to
     make a screenshot "float", and it would be the only shadow on the page.
  */
  .lp-plate { margin: 46px 0 0; }
  .lp-plate--wide { margin-top: 48px; }

  /* Runs to the right edge of the window rather than stopping at the
     container. The rule and radius come off that edge too — a border sitting
     on the viewport edge reads as a mistake, not as a bleed. */
  .lp-plate--bleed {
    margin: 0;
    margin-right: min(-24px, calc((1080px - 100vw) / 2));
    /* The clip runs to the window edge, so without a cap its slot keeps
       growing on a wide monitor while the file stays the size it was recorded
       at — on a 2560 window it was being stretched to 0.63x. The clips are
       1280px wide (see capture.js for why they are not 2x), so 1280 CSS is
       1:1; past that it stays this size and sits flush against the edge
       instead of stretching. Move this and CLIP_VIEW together. */
    max-width: 1280px;
    margin-left: auto;
  }
  .lp-plate--bleed .lp-plate__frame {
    border-right: none;
    border-radius: var(--radius) 0 0 var(--radius);
  }
  .lp-plate--bleed .lp-plate__caption { padding-right: 24px; }
  .lp-plate__frame {
    position: relative;
    border: 1px solid var(--border-mid);
    border-radius: var(--radius);
    overflow: hidden;
    background: var(--bg);
    line-height: 0;      /* no inline descender gap under the media */
  }
  .lp-plate__media {
    display: block;
    width: 100%;
    height: auto;
  }
  .lp-plate__caption {
    margin: 12px 0 0;
    font-size: 13.5px;
    line-height: 1.55;
    color: var(--text-2);
    max-width: 64ch;
  }

  /* Sits over the clip's own paper, so it needs to hold its own against a
     changing frame — hence the solid ground rather than a translucent one. */
  .lp-plate__control {
    position: absolute;
    right: 12px;
    bottom: 12px;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 5px 11px 5px 9px;
    background: var(--bg-raised);
    border: 1px solid var(--border-mid);
    border-radius: var(--radius);
    color: var(--text-2);
    font-family: var(--font);
    font-size: 12.5px;
    line-height: 1;
    cursor: pointer;
  }
  .lp-plate__control:hover { color: var(--text); border-color: var(--text-3); }
  .lp-plate__control:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .lp-plate__control svg { width: 11px; height: 11px; fill: currentColor; }

  .lp-code {
    font-family: var(--font-mono);
    font-size: .88em;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 1px 5px;
    color: var(--text);
    white-space: nowrap;
  }

  /* ── Sections ─────────────────────────────────────────────────────────── */
  .lp-section {
    padding: 92px 24px;
    scroll-margin-top: 64px;
  }
  .lp-section--recessed {
    background: var(--bg-subtle);
    border-top: 1px solid var(--border-mid);
    border-bottom: 1px solid var(--border-mid);
  }
  .lp-section__inner { max-width: 1080px; margin: 0 auto; }
  .lp-section__title {
    font-family: var(--font-display);
    font-size: clamp(27px, 3.4vw, 36px);
    font-weight: 500;
    letter-spacing: -.022em;
    line-height: 1.14;
    margin: 0 0 18px;
    color: var(--text);
    max-width: 20ch;
  }
  .lp-section__lead {
    font-size: 17px;
    line-height: 1.65;
    color: var(--text-2);
    max-width: 48ch;
    margin: 0;
  }

  /* A spread puts the heading in the left column and the content in the
     right, so wide sections carry weight across the measure instead of
     trailing off into empty paper. */
  .lp-spread {
    display: grid;
    grid-template-columns: minmax(0, 360px) minmax(0, 1fr);
    gap: 80px;
    align-items: start;
  }
  .lp-spread .lp-section__title { max-width: none; margin-bottom: 0; }
  .lp-spread .lp-section__lead { margin-top: 18px; }

  /* A media section is a heading, the argument beside it, and the plate under
     both. Running the lead in its own narrow column instead left the right
     half of every band empty. */
  /* The record's band. It sits on the same measure as every other section:
     the shot is portrait and dense enough to carry weight on its own, and an
     earlier pass that widened the container to 1240 only pushed this heading
     80px left of every other heading on the page. */
  .lp-lede {
    display: grid;
    grid-template-columns: minmax(0, 400px) minmax(0, 1fr);
    gap: 72px;
    align-items: start;
  }
  .lp-lede .lp-section__title { max-width: none; margin-bottom: 0; }
  .lp-lede .lp-section__lead { max-width: 58ch; }
  /* Heading and lead sit abreast; the plate spans under both. */
  .lp-lede .lp-plate { grid-column: 1 / -1; }

  /* ── Feature entries: catalog rows, title and call-number on the left ─── */
  .lp-entries {
    margin: 52px 0 0;
    padding: 0;
    border-top: 1px solid var(--border-mid);
  }
  .lp-entry {
    display: grid;
    grid-template-columns: minmax(0, 300px) minmax(0, 1fr);
    gap: 48px;
    padding: 26px 0;
    border-bottom: 1px solid var(--border);
  }
  .lp-entry:last-child { border-bottom-color: var(--border-mid); }
  .lp-entry__head {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .lp-entry__title {
    font-family: var(--font-display);
    font-size: 20px;
    font-weight: 540;
    letter-spacing: -.014em;
    color: var(--text);
  }
  .lp-entry__path {
    font-family: var(--font-mono);
    font-size: 11.5px;
    color: var(--text-3);
  }
  .lp-entry__desc {
    margin: 0;
    font-size: 16px;
    line-height: 1.62;
    color: var(--text-2);
    max-width: 54ch;
  }

  /* ── Sovereignty: a ledger of guarantees, not another feature grid ────── */
  /* Same ruled rows as .lp-entries, but stacked rather than split into two
     columns — the right column of a spread is already narrow, and these read
     as a list of promises rather than a catalog of parts. */
  .lp-guarantees {
    margin: 0;
    padding: 0;
    border-top: 1px solid var(--border-mid);
  }
  .lp-guarantee {
    padding: 20px 0;
    border-bottom: 1px solid var(--border);
  }
  .lp-guarantee:last-child { border-bottom-color: var(--border-mid); }
  .lp-guarantee__head {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 4px 10px;
    margin-bottom: 6px;
  }
  .lp-guarantee__title {
    font-family: var(--font-display);
    font-size: 18px;
    font-weight: 540;
    letter-spacing: -.012em;
    color: var(--text);
  }
  .lp-guarantee__path {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-3);
  }
  .lp-guarantee__desc {
    margin: 0;
    font-size: 15.5px;
    line-height: 1.6;
    color: var(--text-2);
    max-width: 56ch;
  }

  /* ── Editions: three ways to run the same catalog ─────────────────────── */
  /* Three columns divided by a hairline, not three cards. The top rule runs
     the whole width so the three read as one row of the same table. */
  .lp-editions {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    margin-top: 52px;
    border-top: 1px solid var(--border-mid);
  }
  .lp-edition { padding: 26px 34px 4px; }
  .lp-edition:first-child { padding-left: 0; }
  .lp-edition:last-child { padding-right: 0; }
  .lp-edition + .lp-edition { border-left: 1px solid var(--border); }
  .lp-edition__name {
    font-family: var(--font-mono);
    font-size: 11.5px;
    color: var(--text-3);
  }
  .lp-edition__title {
    font-family: var(--font-display);
    font-size: 20px;
    font-weight: 540;
    letter-spacing: -.014em;
    margin: 6px 0 16px;
    color: var(--text);
  }
  .lp-edition__facts { margin: 0; }
  .lp-edition__fact { margin: 0 0 13px; }
  .lp-edition__fact:last-child { margin-bottom: 0; }
  .lp-edition__label {
    font-size: 12.5px;
    color: var(--text-3);
    margin-bottom: 3px;
  }
  .lp-edition__value {
    margin: 0;
    font-size: 15.5px;
    line-height: 1.55;
    color: var(--text-2);
  }
  /* A button for managed (sign up) and a plain link for team (its account is
     where the build comes from), so the two asks do not compete. */
  .lp-edition__cta-btn { margin-top: 20px; font-size: 14px; padding: 8px 16px; }
  .lp-edition__cta-link { display: inline-block; margin-top: 20px; font-size: 14px; }
  .lp-editions__note {
    margin: 34px 0 0;
    font-size: 15.5px;
    line-height: 1.6;
    color: var(--text-2);
  }
  .lp-editions__note .lp-textlink { font-size: inherit; }

  /* ── Steps: a numbered spine, because this actually is a sequence ─────── */
  .lp-steps {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .lp-step {
    display: grid;
    grid-template-columns: 46px minmax(0, 1fr);
    gap: 20px;
    padding: 0 0 32px;
    position: relative;
  }
  /* The spine runs behind the figures and stops at the last one. */
  .lp-step::before {
    content: "";
    position: absolute;
    left: 15px;
    top: 26px;
    bottom: -6px;
    width: 1px;
    background: var(--border-mid);
  }
  .lp-step:last-child { padding-bottom: 0; }
  .lp-step:last-child::before { display: none; }
  .lp-step__n {
    font-family: var(--font-display);
    font-size: 19px;
    font-weight: 540;
    color: var(--accent);
    width: 31px;
    height: 31px;
    border: 1px solid var(--border-mid);
    border-radius: 50%;
    background: var(--bg-subtle);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    position: relative;
    z-index: 1;
  }
  .lp-step__title {
    font-family: var(--font-display);
    font-size: 20px;
    font-weight: 540;
    letter-spacing: -.014em;
    margin: 2px 0 7px;
    color: var(--text);
  }
  .lp-step__desc {
    margin: 0;
    font-size: 16px;
    line-height: 1.62;
    color: var(--text-2);
    max-width: 52ch;
  }

  /* ── Close: the price summary and the final ask are one block, not two ── */
  .lp-close__offer { padding-top: 6px; }
  .lp-close__actions {
    display: flex;
    align-items: center;
    gap: 24px;
    flex-wrap: wrap;
  }
  .lp-facts {
    list-style: none;
    margin: 0 0 32px;
    padding: 0;
    border-top: 1px solid var(--border);
  }
  .lp-fact {
    padding: 14px 0;
    border-bottom: 1px solid var(--border);
    font-size: 16px;
    line-height: 1.55;
    color: var(--text-2);
  }

  /* ── Footer ───────────────────────────────────────────────────────────── */
  .lp-footer {
    padding: 40px 24px 64px;
    border-top: 1px solid var(--border-mid);
  }
  .lp-footer__inner {
    max-width: 1080px;
    margin: 0 auto;
    display: flex;
    align-items: center;
    gap: 24px 40px;
    flex-wrap: wrap;
  }
  .lp-footer__brand { display: inline-flex; align-items: center; gap: 9px; }
  .lp-footer__links {
    display: flex;
    gap: 22px;
    flex-wrap: wrap;
  }
  .lp-footer__link {
    color: var(--text-2);
    text-decoration: none;
    font-size: 14px;
  }
  .lp-footer__link:hover { color: var(--text); }
  .lp-footer__copy {
    margin: 0 0 0 auto;
    font-size: 13px;
    color: var(--text-3);
  }

  /* ── Responsive ───────────────────────────────────────────────────────── */
  @media (max-width: 940px) {
    /* One column, and the clip stops bleeding — at this width there is no
       margin left for it to run into. */
    .lp-hero__inner {
      grid-template-columns: minmax(0, 1fr);
      gap: 44px;
      padding: 60px 0 68px;
    }
    .lp-hero__title { max-width: 14ch; }
    .lp-plate--bleed { margin-right: 0; }
    .lp-plate--bleed .lp-plate__frame {
      border-right: 1px solid var(--border-mid);
      border-radius: var(--radius);
    }
    .lp-plate--bleed .lp-plate__caption { padding-right: 0; }
    .lp-lede {
      grid-template-columns: minmax(0, 1fr);
      gap: 40px;
    }
    .lp-entry {
      grid-template-columns: minmax(0, 1fr);
      gap: 10px;
    }
    .lp-spread {
      grid-template-columns: minmax(0, 1fr);
      gap: 44px;
    }
    /* Three columns become three stacked rows; the dividing rule turns
       horizontal, and the opening top rule would double up with the first
       row's, so it stays as the rule above edition one only. */
    .lp-editions { grid-template-columns: minmax(0, 1fr); }
    .lp-edition { padding: 22px 0 4px; }
    .lp-edition + .lp-edition {
      border-left: none;
      border-top: 1px solid var(--border);
    }
  }
  @media (max-width: 720px) {
    /* Section anchors go; log in and sign up stay, which is what the nav
       is for on a phone. */
    .lp-nav__link--anchor { display: none; }
    .lp-hero { padding: 56px 20px 64px; }
    .lp-hero__title { max-width: none; }
    .lp-section { padding: 64px 20px; }
    .lp-section__title { max-width: none; }
    .lp-plate { margin-top: 32px; }
    .lp-plate__caption { font-size: 13px; }

    /* A 1280px-wide UI squeezed into a phone is a grey smear. The still plates
       keep a legible width and scroll inside their own frame instead — the
       page itself still never scrolls sideways. Clips are left to scale: you
       cannot pan a video while it plays, and motion carries at any size. */
    .lp-plate--still .lp-plate__frame { overflow-x: auto; }
    .lp-plate--still .lp-plate__media { min-width: 860px; }
    .lp-footer__copy { margin-left: 0; flex-basis: 100%; }
  }
</style>
