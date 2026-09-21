const { expect } = require('@playwright/test');
const { test } = require('../fixtures');
const { login } = require('../helpers');

// Scenarios: spec/memory.md (MEM-24)

// Media a record links to but does not contain.
//
// rekam stores records, not blobs. A picture written into a note as a URL is
// fetched by the reader's browser, from someone else's server, when they open
// the record — so it can be gone, and it can be visible to the author and not
// to a teammate whose account cannot see that Drive file. This test pins the
// behaviour that makes that honest rather than confusing:
//
//   * a bare image or clip URL becomes the media, the way a bare YouTube link
//     already became a player — pasting a URL is how people actually write
//   * a link someone deliberately worded stays a link
//   * a share link that can never render inline (Drive, Dropbox) is left as a
//     link rather than turned into a broken image
//   * an image that fails says whose server it was on and that rekam does not
//     store it, instead of showing the browser's broken-image icon and reading
//     as rekam's bug
//   * nothing externally hosted leaks the reader's page to its host
//
// The seeded hosts are .invalid (RFC 2606), so the failures are deterministic
// and need no network: the test depends on them NOT resolving.
test('24-external-media', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  const row = page.locator('.record').filter({ hasText: 'Screens from the launch review' }).first();
  if (await row.count() === 0) throw new Error("seeded record 'Screens from the launch review' not found");
  await row.click();
  await page.locator('.markdown-body').waitFor({ state: 'visible', timeout: 10000 });
  // Give the failing image fetches time to resolve into their dead-link cards.
  await page.waitForTimeout(3000);

  const body = page.locator('.markdown-body');

  // A bare clip URL became a player, not a link.
  const video = body.locator('.media-embed video');
  await expect(video).toHaveCount(1);
  await expect(video).toHaveAttribute('referrerpolicy', 'no-referrer');
  await expect(video).toHaveAttribute('preload', 'none');
  log('a pasted .mp4 URL renders as a player, with no referrer leaked');

  // Two images were externally hosted and could not load — one pasted bare,
  // one written as ![](…). Both say the same thing.
  const dead = body.locator('.media-dead');
  await expect(dead).toHaveCount(2);
  for (const card of await dead.all()) {
    await expect(card).toContainText('example.invalid');
    await expect(card).toContainText('not stored here');
    await expect(card.locator('a')).toHaveAttribute('rel', /noreferrer/);
  }
  log('both unreachable images explain where they were hosted and that rekam does not store them');

  // A <div> inside a <p> would make the browser split the paragraph around it;
  // the dead-link card replaces the whole block instead.
  expect(await body.locator('p div').count()).toBe(0);

  // A share link that cannot render inline stays a link. Turning it into an
  // <img> would produce a permanent broken image, because the URL serves a web
  // page rather than a picture.
  const drive = body.locator('a[href*="drive.google.com"]');
  await expect(drive).toHaveCount(1);
  await expect(body.locator('img[src*="drive.google.com"]')).toHaveCount(0);
  log('a Drive share link is left as a link, not turned into a broken image');

  // A link someone worded on purpose is not replaced by its target.
  const worded = body.locator('a', { hasText: 'the original diagram' });
  await expect(worded).toHaveCount(1);
  log('a deliberately worded link stays a link');

  await snap('24-external-media');
});
