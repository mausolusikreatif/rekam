const { expect } = require('@playwright/test');
const { test } = require('../fixtures');
const { login } = require('../helpers');

// Creating a team: from the workspace switcher, open "New team", name it, create
// it, and confirm the switcher lands in the new workspace.
test('16-create-team', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  const teamSwitcher = page.locator('.switcher', { has: page.locator('[title="Switch workspace"]') });
  await teamSwitcher.locator('.current').click();
  await teamSwitcher.locator('.menu').waitFor({ state: 'visible', timeout: 8000 });
  await page.waitForTimeout(300);

  await teamSwitcher.locator('.menu__action', { hasText: 'New team' }).click();
  const nameInput = teamSwitcher.locator('.create-form input');
  await nameInput.waitFor({ state: 'visible', timeout: 6000 });
  await snap('01-new-team-form');

  const NAME = 'Field Notes QA';
  await nameInput.fill(NAME);
  await page.waitForTimeout(300);
  await teamSwitcher.locator('.btn-create', { hasText: 'Create' }).click();

  // Creation jumps straight into the new team; the switcher label updates. Wait
  // on the label rather than the clock — creating a team provisions a whole new
  // corpus file, which is occasionally slower than any fixed pause.
  const label = teamSwitcher.locator('.current__name');
  await expect(label).toHaveText(NAME, { timeout: 15000 });
  log(`Active workspace after create: ${(await label.textContent())?.trim()}`);
  await page.waitForTimeout(400); // let the view settle before the shot
  await snap('02-created-team');
});
