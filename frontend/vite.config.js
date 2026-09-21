import { rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { VitePWA } from 'vite-plugin-pwa';

// Which edition this bundle is for; mirrors the server's build tags.
const edition = process.env.REKAM_EDITION || 'managed';
const emptyComponent = fileURLToPath(
  new URL('./src/lib/EmptyEdition.svelte', import.meta.url),
);

export default defineConfig({
  // Which edition this bundle is for — see src/lib/edition.js. Mirrors the
  // server's build tags so the UI never offers a route the binary lacks.
  // Defaults to 'managed', matching `go build` with no tags.
  define: {
    __REKAM_EDITION__: JSON.stringify(edition),
    // Only read by the landing page, which this bundle no longer contains —
    // defined so the module still resolves if anything imports it, and so the
    // two builds cannot silently disagree about what the names mean.
    __REKAM_MEDIA_BASE__: JSON.stringify('/ui/media'),
    __REKAM_APP_BASE__: JSON.stringify(''),
    __REKAM_DOCS_URL__: JSON.stringify('/docs'),
  },

  // Stub out components an edition does not ship, so a solo bundle does not
  // carry the admin console's markup for a route its server lacks. The call
  // sites are gated by lib/edition.js as well — this stops the code being
  // shipped at all, rather than merely unreachable.
  resolve: {
    alias: [
      ...(edition === 'managed'
        ? []
        : [{ find: /^.*\/AdminConsole\.svelte$/, replacement: emptyComponent }]),
      ...(edition === 'managed'
        ? []
        : [{ find: /^.*\/BillingReturn\.svelte$/, replacement: emptyComponent }]),
      ...(edition === 'managed' ? [] : [{ find: /^.*\/DocsShell\.svelte$/, replacement: emptyComponent }]),
      // The landing page and the legal documents left this bundle entirely:
      // they are their own static site now (vite.marketing.config.js), served
      // at the origin root. Aliased rather than merely unrouted so no edition
      // ships the markup — and so an accidental import fails loudly in review
      // rather than quietly re-adding 300KB to the app.
      { find: /^.*\/LandingPage\.svelte$/, replacement: emptyComponent },
      { find: /^.*\/LegalPage\.svelte$/, replacement: emptyComponent },
      ...(edition === 'solo'
        ? [{ find: /^.*\/TeamSwitcher\.svelte$/, replacement: emptyComponent }]
        : []),
    ],
  },

  plugins: [
    // The marketing videos (~6.6MB across three themes) live in public/, so
    // Vite copies them verbatim into every build. Nothing in this bundle
    // renders them any more — the landing page that did is its own site now —
    // so every edition drops them, and no binary carries megabytes of
    // advertising inside it.
    {
      name: 'rekam:drop-marketing-media',
      apply: 'build',
      closeBundle() {
        rmSync(fileURLToPath(new URL('../internal/api/web/media', import.meta.url)), {
          recursive: true,
          force: true,
        });
      },
    },
    svelte(),
    VitePWA({
      registerType: 'autoUpdate',
      includeAssets: ['icon-192.png', 'icon-512.png', 'icon-maskable-512.png', 'apple-touch-icon.png'],
      manifest: {
        id: '/ui/',
        name: 'rekam — the catalog of records',
        short_name: 'rekam',
        description: 'Persistent memory for you and your agents — a catalog of records.',
        start_url: '/ui/',
        scope: '/ui/',
        display: 'standalone',
        theme_color: '#2e4a78',
        background_color: '#f6f2e9',
        icons: [
          { src: 'icon-192.png', sizes: '192x192', type: 'image/png' },
          { src: 'icon-512.png', sizes: '512x512', type: 'image/png' },
          { src: 'icon-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
        ],
      },
      workbox: {
        // Precache the whole app shell — including the lazy editor chunk and the
        // self-hosted fonts — so an installed rekam opens instantly and the UI
        // works offline. (Live record data still needs the network.)
        globPatterns: ['**/*.{js,css,html,svg,png,woff2}'],
        navigateFallback: '/ui/index.html',
        // API + OAuth live outside the SW scope (/ui/), but guard anyway.
        navigateFallbackDenylist: [/^\/(me|memories|memory|catalog|search|identities|scope|admin|export|mcp|public)\b/],
        maximumFileSizeToCacheInBytes: 3 * 1024 * 1024,
        cleanupOutdatedCaches: true,
      },
    }),
  ],
  base: '/ui/',
  build: {
    outDir: '../internal/api/web',
    emptyOutDir: true,
    chunkSizeWarningLimit: 600,
  },
  server: {
    proxy: {
      '/me': 'http://localhost:5000',
      '/memories': 'http://localhost:5000',
      '/memory': 'http://localhost:5000',
      '/catalog': 'http://localhost:5000',
      '/export': 'http://localhost:5000',
      '/search': 'http://localhost:5000',
      '/identities': 'http://localhost:5000',
      '/scope': 'http://localhost:5000',
      '/admin': 'http://localhost:5000',
    },
  },
});
