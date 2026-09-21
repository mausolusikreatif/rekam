<script>
  import { activeKey, identities, SESSION_KEY } from '../lib/store.js';
  import { hasControlPlane } from '../lib/edition.js';
  import { setPageTitle } from '../lib/page-title.js';
  import {
    apiLogin,
    apiSignup,
    apiConfirm,
    apiForgot,
    apiReset,
  } from '../lib/api.js';

  // Six states share one sheet. Each names itself in a single display line and
  // one sentence of lead; the rail, the margin rule, and the column never move,
  // so crossing between states reads as turning a page rather than as opening a
  // different screen.
  const COPY = {
    'login': {
      tab: 'Log in',
      title: 'Open your catalog.',
      lead: 'The memory your agents read and write — yours, not the model vendor\'s. Same records whether you are here solo or with a team.',
      submit: 'Log in',
      busy: 'Logging in…',
    },
    'signup': {
      tab: 'Create an account',
      title: 'Start your catalog.',
      lead: 'Yours from the first write. Work solo now and add a team later without moving anything — and if you host it yourself, you keep the keys the whole time.',
      submit: 'Create account',
      busy: 'Creating account…',
    },
    'confirm-wait': {
      tab: 'Confirm your email',
      title: 'Confirm your email.',
      step: 'Step 2 of 2',
      submit: 'Confirm and open',
      busy: 'Confirming…',
    },
    'confirm-enter': {
      tab: 'Confirm your email',
      title: 'Confirm your email.',
      lead: 'This account was created but never confirmed. Paste the code from the confirmation email to finish.',
      submit: 'Confirm and open',
      busy: 'Confirming…',
    },
    'forgot': {
      tab: 'Reset your password',
      title: 'Reset your password.',
      lead: 'Enter the email on the account and we will send a reset code.',
      submit: 'Send reset code',
      busy: 'Sending…',
    },
    'reset': {
      tab: 'Set a new password',
      title: 'Set a new password.',
      step: 'Step 2 of 2',
      submit: 'Set password and log in',
      busy: 'Setting…',
    },
    'key': {
      tab: 'Open with an API key',
      title: 'Open with an API key.',
      // Lead written in the markup instead: it sets `rekam install` as code.
      submit: 'Open the catalog',
      busy: 'Opening…',
    },
  };

  // Which state to open on. The landing page's "Start free" asks for signup;
  // "Log in" asks for login. Everything after that is the gate's own business.
  export let initial = 'login';

  let mode = COPY[initial] ? initial : 'login';
  let email = '';
  let password = '';
  let name = '';
  let keyInput = '';
  let error = '';
  let loading = false;
  let confirmCode = '';
  let confirmEmail = '';
  let resetToken = '';
  let resetEmail = '';
  let newPassword = '';
  let info = '';
  // True when the server told us it could not send the mail and handed back the
  // token instead. That is a fact about this deployment, not a message to the
  // person, so it gets said plainly rather than dumped in as a raw URL.
  let unsent = false;

  $: copy = COPY[mode];
  // The tab follows the state, so a half-finished signup does not sit there
  // calling itself "Log in".
  $: setPageTitle(copy.tab);

  function enter(identity, key) {
    identities.set([{ key, ...identity }]);
    activeKey.set(key);
  }

  // The API's own error strings are already written for a person ("an account
  // with that email already exists", "password must be 8–72 characters"), so
  // they are shown as written rather than remapped to guesses that would drift
  // the moment the server changes.
  function message(err, fallback) {
    const raw = err?.error || err?.message || '';
    if (!raw) return fallback;
    return raw.charAt(0).toUpperCase() + raw.slice(1);
  }

  async function submitLogin() {
    if (!email || !password) return;
    try {
      const result = await apiLogin(email, password);
      enter(result.identity, SESSION_KEY);
    } catch (err) {
      if (err?.error === 'email not confirmed') {
        confirmEmail = email;
        go('confirm-enter');
        return;
      }
      error = message(err, 'Login failed');
    }
  }

  async function submitSignup() {
    if (!email || !password) return;
    const result = await apiSignup(email, password, name);
    if (result.confirm_code || result.email_sent === false) {
      confirmEmail = email;
      // No mailer on this server: the code comes back in the response, so fill
      // it in rather than making someone copy it out of a URL.
      confirmCode = result.confirm_code || '';
      unsent = !!result.confirm_code;
      mode = 'confirm-wait';
      error = '';
      info = '';
      return;
    }
    if (result.identity && result.status !== 'unconfirmed') {
      enter(result.identity, SESSION_KEY);
      return;
    }
    confirmEmail = email;
    mode = 'confirm-wait';
  }

  async function submitConfirm() {
    if (!confirmCode) return;
    const result = await apiConfirm(confirmCode);
    if (result.authenticated && result.identity) enter(result.identity, SESSION_KEY);
  }

  async function submitForgot() {
    if (!resetEmail) return;
    const result = await apiForgot(resetEmail);
    if (result.reset_code) {
      resetToken = result.reset_code;
      unsent = true;
      go('reset');
      return;
    }
    // Whether or not the address has an account, the answer is the same one.
    info = `If an account exists for ${resetEmail}, a reset code is on its way.`;
  }

  async function submitReset() {
    if (!resetToken || !newPassword) return;
    const result = await apiReset(resetToken, newPassword);
    if (result.authenticated && result.identity) enter(result.identity, SESSION_KEY);
  }

  function submitKey() {
    if (!keyInput) return;
    enter({ email: keyInput }, keyInput);
  }

  const HANDLERS = {
    'login': submitLogin,
    'signup': submitSignup,
    'confirm-wait': submitConfirm,
    'confirm-enter': submitConfirm,
    'forgot': submitForgot,
    'reset': submitReset,
    'key': submitKey,
  };

  // One submit path for every state, so the form element does the work the
  // browser and password managers expect of it.
  async function submit() {
    if (loading) return;
    error = '';
    info = '';
    loading = true;
    try {
      await HANDLERS[mode]();
    } catch (err) {
      error = message(err, 'Something went wrong. Try again.');
    } finally {
      loading = false;
    }
  }

  function go(m) {
    mode = m;
    error = '';
    info = '';
    if (m !== 'reset' && m !== 'confirm-wait') unsent = false;
  }

  // Each state mounts its own first field, so focus lands there without the
  // autofocus attribute fighting between branches.
  function focusHere(node) {
    node.focus();
  }
</script>

<div class="gate">
  <header class="gate__rail">
    <a class="gate__brand" href="#/">
      <span class="gate__mark">R</span>
      <span class="gate__wordmark">rekam</span>
    </a>
    <a class="gate__back" href="#/">Back to the site</a>
  </header>

  <main class="gate__page">
    <div class="gate__col">
      {#if copy.step}
        <p class="gate__step">{copy.step}</p>
      {/if}
      <h1 class="gate__title">{copy.title}</h1>

      {#if mode === 'confirm-wait'}
        <p class="gate__lead">
          {#if unsent}
            This server has no mailer configured, so the code is filled in below.
          {:else}
            We sent a code to <span class="gate__email">{confirmEmail}</span>. Paste it here to finish.
          {/if}
        </p>
      {:else if copy.lead || mode === 'key'}
        <p class="gate__lead">
          {#if mode === 'key'}
            Paste the key <code class="gate__code">rekam install</code> printed. It opens the same catalog a password does.
          {:else}
            {copy.lead}
          {/if}
        </p>
      {/if}

      <form class="gate__form" on:submit|preventDefault={submit}>
        {#if mode === 'login'}
          <div class="gate__field">
            <label for="gate-email">Email</label>
            <input id="gate-email" type="email" bind:value={email}
                   autocomplete="email" use:focusHere />
          </div>
          <div class="gate__field">
            <div class="gate__labelrow">
              <label for="gate-pw">Password</label>
              <button type="button" class="gate__inline" on:click={() => go('forgot')}>Forgot it?</button>
            </div>
            <input id="gate-pw" type="password" bind:value={password}
                   autocomplete="current-password" />
          </div>

        {:else if mode === 'signup'}
          <div class="gate__field">
            <label for="su-name">Name</label>
            <input id="su-name" type="text" bind:value={name}
                   autocomplete="name" use:focusHere />
          </div>
          <!-- rekam records who stood behind every write, so this field is the
               byline on the whole corpus rather than a nicety. Showing it as one
               makes the case for filling it in better than a hint would. -->
          <p class="gate__byline" class:is-signed={name.trim()}>
            <span class="gate__byline-name">{name.trim() || 'Your name'}</span>
            <span class="gate__byline-note">signs every record your agents write</span>
          </p>
          <div class="gate__field">
            <label for="su-email">Email</label>
            <input id="su-email" type="email" bind:value={email} autocomplete="email" />
          </div>
          <div class="gate__field">
            <label for="su-pw">Password</label>
            <input id="su-pw" type="password" bind:value={password}
                   autocomplete="new-password" />
            <p class="gate__hint">At least 8 characters.</p>
          </div>

        {:else if mode === 'confirm-wait' || mode === 'confirm-enter'}
          <div class="gate__field">
            <label for="confirm-code">Confirmation code</label>
            <input id="confirm-code" class="gate__syntax" type="text" bind:value={confirmCode}
                   autocomplete="one-time-code" spellcheck="false" use:focusHere />
          </div>

        {:else if mode === 'forgot'}
          <div class="gate__field">
            <label for="forgot-email">Email</label>
            <input id="forgot-email" type="email" bind:value={resetEmail}
                   autocomplete="email" use:focusHere />
          </div>

        {:else if mode === 'reset'}
          {#if unsent}
            <p class="gate__lead gate__lead--tight">
              This server has no mailer configured, so the reset code is filled in below.
            </p>
          {/if}
          <div class="gate__field">
            <label for="reset-token">Reset code</label>
            <input id="reset-token" class="gate__syntax" type="text" bind:value={resetToken}
                   autocomplete="one-time-code" spellcheck="false" />
          </div>
          <div class="gate__field">
            <label for="new-pw">New password</label>
            <input id="new-pw" type="password" bind:value={newPassword}
                   autocomplete="new-password" use:focusHere />
            <p class="gate__hint">At least 8 characters.</p>
          </div>

        {:else}
          <div class="gate__field">
            <label for="gate-key">API key</label>
            <input id="gate-key" class="gate__syntax" type="text" bind:value={keyInput}
                   placeholder="mkey_…" autocomplete="off" spellcheck="false" use:focusHere />
          </div>
        {/if}

        <button class="gate__submit" type="submit" disabled={loading}>
          {loading ? copy.busy : copy.submit}
        </button>

        {#if error}<p class="gate__error" role="alert">{error}</p>{/if}
        {#if info}<p class="gate__info">{info}</p>{/if}
      </form>

      <div class="gate__alts">
        {#if mode === 'login'}
          {#if hasControlPlane}
            <button type="button" class="gate__alt" on:click={() => go('signup')}>Create an account</button>
          {/if}
          <button type="button" class="gate__alt gate__alt--quiet" on:click={() => go('key')}>Open with an API key instead</button>
        {:else if mode === 'signup'}
          <button type="button" class="gate__alt" on:click={() => go('login')}>Log in instead</button>
        {:else if mode === 'forgot'}
          <button type="button" class="gate__alt" on:click={() => go('reset')}>I already have a code</button>
          <button type="button" class="gate__alt gate__alt--quiet" on:click={() => go('login')}>Back to log in</button>
        {:else}
          <button type="button" class="gate__alt" on:click={() => go('login')}>Back to log in</button>
        {/if}
      </div>
    </div>
  </main>
</div>

<style>
  /* ═══════════════════════════════════════════════════════════════════════
     The gate.

     A page of the catalog, not a card on a backdrop: the paper runs full
     bleed and a rail carries the wordmark exactly where the app's toolbar will
     be, so the brand does not move when the form succeeds. The column is
     centred and the rail's rule is the only structure it needs — an earlier
     pass hung it off a full-height ruled margin pinned to the sidebar's width,
     which read as a stripe through the page rather than as a margin.

     Mono appears only on fields whose content is literally machine syntax —
     API keys and tokens — never as a label face. That means overriding the
     global .form-group label voice, which is why these fields carry their own
     class instead of reusing it.

     Text colours here stop at --text-2. --text-3 measures about 2.8:1 on the
     bone paper, which is fine for catalog meta but not for a control someone
     has to find in order to get in.
     ═══════════════════════════════════════════════════════════════════════ */

  .gate {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  /* ── Rail ─────────────────────────────────────────────────────────────── */
  .gate__rail {
    flex-shrink: 0;
    height: var(--toolbar-h);
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    /* Same height, padding and gap as Sidebar's header, so the mark and
       wordmark sit on exactly the same pixels before and after login. */
    padding: 0 18px;
    border-bottom: 1px solid var(--border);
  }
  .gate__brand {
    display: inline-flex;
    align-items: center;
    gap: 9px;
    text-decoration: none;
    color: var(--text);
  }
  .gate__mark {
    width: 26px;
    height: 26px;
    background: var(--accent);
    color: var(--accent-text);
    border-radius: var(--radius);
    box-shadow: inset 0 0 0 1px rgba(255,255,255,.12), var(--shadow-sm);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-display);
    font-weight: 600;
    font-size: 15px;
    flex-shrink: 0;
  }
  .gate__wordmark {
    font-family: var(--font-display);
    font-size: 20px;
    font-weight: 560;
    letter-spacing: -.01em;
  }
  .gate__back {
    font-size: 13.5px;
    color: var(--text-2);
    text-decoration: none;
  }
  .gate__back:hover { color: var(--text); text-decoration: underline; }

  /* ── Page ─────────────────────────────────────────────────────────────── */
  .gate__page {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    display: flex;
    justify-content: center;
    padding: 0 22px;
  }
  .gate__col {
    width: 100%;
    max-width: 400px;
    padding: clamp(36px, 9vh, 96px) 0 48px;
  }

  /* ── Heading ──────────────────────────────────────────────────────────── */
  .gate__step {
    margin: 0 0 10px;
    font-size: 13px;
    color: var(--text-2);
  }
  .gate__title {
    margin: 0 0 10px;
    font-family: var(--font-display);
    font-size: 31px;
    font-weight: 540;
    line-height: 1.15;
    letter-spacing: -.02em;
    color: var(--text);
  }
  .gate__lead {
    margin: 0 0 30px;
    max-width: 34em;
    font-size: 15px;
    line-height: 1.6;
    color: var(--text-2);
  }
  /* The same note inside the form rather than above it, so it keeps the lead's
     voice without the lead's gap. */
  .gate__lead--tight { margin-bottom: 20px; }
  .gate__email { color: var(--text); }
  .gate__code {
    font-family: var(--font-mono);
    font-size: 13px;
    color: var(--text);
  }

  /* ── Fields ───────────────────────────────────────────────────────────── */
  .gate__field {
    display: flex;
    flex-direction: column;
    gap: 5px;
    margin-bottom: 18px;
  }
  .gate__labelrow {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
  }
  .gate__field label {
    font-family: var(--font);
    font-size: 13.5px;
    font-weight: 500;
    letter-spacing: 0;
    text-transform: none;
    color: var(--text-2);
  }
  /* Descendant selector so this beats global.css's input[type="…"] rules
     without needing !important. */
  .gate__field input {
    width: 100%;
    background: var(--bg-subtle);
    border: 1px solid transparent;
    border-bottom: 1px solid var(--border-mid);
    border-radius: 0;
    padding: 9px 11px;
    font-family: var(--font);
    font-size: 15px;
    color: var(--text);
    outline: none;
    transition: box-shadow var(--transition), background var(--transition);
  }
  .gate__field input:hover { background: var(--bg-hover); }
  /* Inset rather than a thicker border, so focus never shifts the layout. */
  .gate__field input:focus {
    background: var(--bg-raised);
    border-bottom-color: var(--accent);
    box-shadow: inset 0 -2px 0 var(--accent);
  }
  .gate__field input.gate__syntax {
    font-family: var(--font-mono);
    font-size: 13.5px;
  }
  .gate__hint {
    margin: 1px 0 0;
    font-size: 12.5px;
    color: var(--text-2);
  }

  .gate__inline {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    font-family: var(--font);
    font-size: 12.5px;
    color: var(--text-2);
  }
  .gate__inline:hover { color: var(--accent); text-decoration: underline; }

  /* ── The byline ───────────────────────────────────────────────────────── */
  /* No rule of its own: the field's own bottom rule already closes the row
     above, and a second hairline 4px under it just reads as noise. */
  .gate__byline {
    margin: 8px 0 22px;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .gate__byline-name {
    font-family: var(--font-display);
    font-size: 21px;
    font-weight: 540;
    letter-spacing: -.015em;
    color: var(--text-2);
    transition: color var(--transition);
  }
  .gate__byline.is-signed .gate__byline-name { color: var(--text); }
  .gate__byline-note {
    font-size: 12.5px;
    color: var(--text-2);
  }

  /* ── Submit ───────────────────────────────────────────────────────────── */
  .gate__submit {
    margin-top: 6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 9px 22px;
    background: var(--accent);
    color: var(--accent-text);
    border: 1px solid var(--accent);
    border-radius: var(--radius);
    font-family: var(--font);
    font-size: 14.5px;
    font-weight: 560;
    cursor: pointer;
    transition: background var(--transition), border-color var(--transition);
  }
  .gate__submit:hover:not(:disabled) {
    background: var(--accent-hover);
    border-color: var(--accent-hover);
  }
  .gate__submit:disabled { opacity: .45; cursor: not-allowed; }

  .gate__error {
    margin: 14px 0 0;
    font-size: 13.5px;
    color: var(--stamp);
  }
  .gate__info {
    margin: 14px 0 0;
    font-size: 13.5px;
    line-height: 1.55;
    color: var(--text-2);
  }

  /* ── Alternates ───────────────────────────────────────────────────────── */
  .gate__alts {
    margin-top: 26px;
    padding-top: 16px;
    border-top: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }
  .gate__alt {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    font-family: var(--font);
    font-size: 14px;
    text-align: left;
    color: var(--accent);
  }
  .gate__alt:hover { text-decoration: underline; }
  .gate__alt--quiet { font-size: 13px; color: var(--text-2); }
  .gate__alt--quiet:hover { color: var(--text); }

  /* ── Focus ────────────────────────────────────────────────────────────── */
  .gate__brand:focus-visible,
  .gate__back:focus-visible,
  .gate__inline:focus-visible,
  .gate__alt:focus-visible,
  .gate__submit:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 3px;
    border-radius: var(--radius);
  }

  /* ── Mobile ───────────────────────────────────────────────────────────── */
  @media (max-width: 600px) {
    .gate__page { padding: 0 18px; }
    .gate__col {
      padding: 40px 0;
      max-width: 100%;
    }
    .gate__title { font-size: 27px; }
    .gate__field input { font-size: 16px; }
    .gate__field input.gate__syntax { font-size: 15px; }
    .gate__submit { width: 100%; }
  }
</style>
