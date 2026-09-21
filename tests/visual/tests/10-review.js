const { test } = require('../fixtures');
const { login } = require('../helpers');

// Spaced-repetition review: the seed enrolled a record, so /review shows a card
// that can be revealed and graded.
test('10-review', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  await page.goto(`${baseUrl}/#/review`, { waitUntil: 'networkidle' });
  await page.locator('.review-page').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(600);
  await snap('01-review');

  const reveal = page.locator('.reveal');
  if (await reveal.count() === 0) {
    log('No due cards — review deck empty (seed may not have enrolled one)');
    throw new Error('expected a seeded due card in the review deck');
  }
  await reveal.click();
  await page.waitForTimeout(500);
  await snap('02-revealed');

  const grades = page.locator('.grade');
  const n = await grades.count();
  log(`Grade options: ${n}`);
  if (n === 0) throw new Error('revealed card has no grade buttons');
  await grades.first().click();
  await page.waitForTimeout(700);
  await snap('03-graded');
});
