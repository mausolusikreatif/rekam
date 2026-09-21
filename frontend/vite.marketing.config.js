import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { marked } from 'marked';

// The marketing site: the landing page and the two legal documents, built as
// static pages served from the site root rather than from inside the app
// bundle.
//
// Why it is a separate build at all: a stranger who types rekam.net was
// downloading the whole application — ~320KB of gzipped JavaScript, the
// editor, the graph renderer, katex, mermaid — and executing it before the
// pitch painted, and a crawler saw an empty <div id="app">. The page whose
// only job is to convince someone who has never heard of rekam was the worst
// served page on the site.
//
// It stays inside frontend/ rather than becoming its own project so it keeps
// using the app's tokens and global.css. The landing page's whole design
// argument is that it looks like the product; a second copy of the design
// system is how that stops being true.

// Four separate HTML documents rather than one page with a client router:
// /terms, /privacy and /pricing are exactly the kind of thing a person links
// to directly and a crawler indexes, and a hash route gives them all the same
// URL.
const page = (name) => fileURLToPath(new URL(`./marketing/${name}.html`, import.meta.url));
const frontendRoot = fileURLToPath(new URL('.', import.meta.url));
const marketingEntry = (name) => `/@fs${fileURLToPath(new URL(`./src/marketing/${name}.js`, import.meta.url))}`;

/**
 * The marketing HTML lives under frontend/marketing so production can build a
 * static site rooted at /. In dev, though, a root request for / makes
 * ../src/marketing/landing.js resolve to /src/... in the browser, which is
 * inside frontend/marketing and therefore blank. Repoint those four entry
 * scripts to Vite's filesystem URL only while serving locally.
 */
function serveMarketingEntries() {
  return {
    name: 'rekam:serve-marketing-entries',
    apply: 'serve',
    transformIndexHtml(html) {
      return html
        .replace('../src/marketing/landing.js', marketingEntry('landing'))
        .replace('../src/marketing/terms.js', marketingEntry('terms'))
        .replace('../src/marketing/privacy.js', marketingEntry('privacy'))
        .replace('../src/marketing/pricing.js', marketingEntry('pricing'));
    },
  };
}

/**
 * Renders `*.md?html` to an HTML string at build time.
 *
 * The legal pages are prose that cannot change between builds, so shipping a
 * markdown renderer to parse them in the reader's browser buys nothing — and
 * the app's MarkdownPreview would bring katex, mermaid and DOMPurify along
 * for the ride. The markdown is ours, from the repo, so there is nothing to
 * sanitize at runtime.
 */
function markdownToHtml() {
  return {
    name: 'rekam:md-to-html',
    enforce: 'pre',
    async transform(code, id) {
      if (!id.endsWith('.md?html')) return null;
      return {
        code: `export default ${JSON.stringify(marked.parse(code))};`,
        map: null,
      };
    },
    async load(id) {
      if (!id.endsWith('.md?html')) return null;
      const { readFile } = await import('node:fs/promises');
      return readFile(id.slice(0, -'?html'.length), 'utf8');
    },
  };
}

/**
 * Publishes release/version.json at the site root.
 *
 * This is the file every self-hosted instance polls to learn that a new build
 * exists — the only channel there is for telling an operator about a security
 * fix (internal/updatecheck, issue #35). It rides with the marketing site
 * rather than being embedded in the managed binary so that publishing a solo
 * release does not require deploying rekam.net to announce it.
 */
function publishVersionManifest() {
  const source = fileURLToPath(new URL('../release/version.json', import.meta.url));
  return {
    name: 'rekam:version-manifest',
    apply: 'build',
    async generateBundle() {
      const { readFile } = await import('node:fs/promises');
      const raw = await readFile(source, 'utf8');
      JSON.parse(raw); // fail the build rather than serve a manifest nobody can parse
      this.emitFile({ type: 'asset', fileName: 'version.json', source: raw });
    },
  };
}

// Full split: rekam.net is the static marketing site and nothing else — the
// app, its login/signup, and its docs mirror all live at their own origin.
// That origin is a build-time constant, not a relative path, because there
// is no proxy between the two anymore for a relative path to resolve
// against (see edge/wrangler.toml). Overridable so the test suite can point
// a built marketing bundle at its own local app instance instead of the
// real rekam.net — see tests/visual/run.sh, which sets this before building.
const APP_ORIGIN = process.env.REKAM_APP_ORIGIN || 'https://app.rekam.net';

export default defineConfig({
  root: fileURLToPath(new URL('./marketing', import.meta.url)),
  publicDir: fileURLToPath(new URL('./public', import.meta.url)),
  base: '/',

  define: {
    // The marketing build is the hosted service's shop window; there is no
    // such thing as a self-hosted landing page.
    __REKAM_EDITION__: JSON.stringify('managed'),
    // Media sits at the site root here, not under the app's /ui/ prefix.
    __REKAM_MEDIA_BASE__: JSON.stringify('/media'),
    __REKAM_APP_BASE__: JSON.stringify(`${APP_ORIGIN}/ui/`),
    __REKAM_DOCS_URL__: JSON.stringify(`${APP_ORIGIN}/docs`),
  },

  plugins: [serveMarketingEntries(), markdownToHtml(), publishVersionManifest(), svelte()],

  server: {
    fs: { allow: [frontendRoot] },
  },

  build: {
    outDir: fileURLToPath(new URL('../dist/marketing', import.meta.url)),
    emptyOutDir: true,
    rollupOptions: {
      input: { index: page('index'), terms: page('terms'), privacy: page('privacy'), pricing: page('pricing') },
    },
  },
});
