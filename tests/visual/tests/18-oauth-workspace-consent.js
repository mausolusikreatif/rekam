const { expect } = require('@playwright/test');
const { test } = require('../fixtures');
const { login, ADMIN_EMAIL, ADMIN_PASSWORD } = require('../helpers');

// The OAuth consent flow a Claude connector drives: /authorize is a plain
// server-rendered page, not part of the SPA, so this is the only place it gets
// exercised in a browser. The seeded admin belongs to two workspaces (Personal +
// Acme Research), which is exactly the case that used to strand a user in
// personal memory with no way to reach their team.
test('18-oauth-workspace-consent', async ({ page, baseUrl, snap, log }) => {
  // A real PKCE challenge, so the flow is the one claude.ai actually performs.
  // Verifier and challenge are fixed here; the browser never needs the verifier.
  const VERIFIER = 'dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk';
  const challenge = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM'; // S256 of VERIFIER

  const params = new URLSearchParams({
    redirect_uri: `${baseUrl}/oauth-visual-callback`,
    state: 'visual-test-state',
    code_challenge: challenge,
    code_challenge_method: 'S256',
    client_id: 'claude-ai',
  });

  await page.goto(`${baseUrl}/authorize?${params}`, { waitUntil: 'networkidle' });
  await page.locator('#email').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(300);
  await snap('01-authorize-login');

  await page.fill('#email', ADMIN_EMAIL);
  await page.fill('#pw', ADMIN_PASSWORD);
  await page.waitForTimeout(200);
  await page.click('button[type="submit"]');

  // Step two: the workspace picker. Both corpora must be offered, each with the
  // user's role in it.
  await page.locator('input[name="team_id"]').first().waitFor({ state: 'visible', timeout: 10000 });
  const labels = await page.locator('form label').allInnerTexts();
  const flat = labels.map((l) => l.replace(/\s+/g, ' ').trim());
  log(`Workspaces offered: ${flat.join(' | ')}`);
  if (!flat.some((l) => l.includes('Personal memory'))) throw new Error('personal memory not offered');
  if (!flat.some((l) => l.includes('Acme Research'))) throw new Error('the team was not offered');
  await page.waitForTimeout(300);
  await snap('02-workspace-picker');

  // Choose the team rather than the default, so the assertion below proves the
  // choice was honoured and not just that some code came back.
  await page.locator('form label').filter({ hasText: 'Acme Research' }).click();
  await page.waitForTimeout(300);
  await snap('03-team-selected');

  // Assert on the redirect the server issues, read straight off the 302. Where
  // the browser ends up afterwards is the client's business and, for a callback
  // URL this server doesn't serve, just the marketing page.
  const [redirectResponse] = await Promise.all([
    page.waitForResponse((r) => r.url().includes('/authorize/workspace') && r.status() === 302),
    page.click('button[type="submit"]'),
  ]);
  const location = redirectResponse.headers()['location'];
  if (!location) throw new Error('the consent form did not redirect to the client callback');
  const redirected = new URL(location);
  const code = redirected.searchParams.get('code');
  if (!code) throw new Error(`callback carried no authorization code: ${location}`);
  log(`Redirected with code=${code.slice(0, 8)}… state=${redirected.searchParams.get('state')}`);
  if (redirected.searchParams.get('state') !== 'visual-test-state') {
    throw new Error('state parameter did not survive the flow');
  }

  // Finish the exchange the way the client's backend would. Until this runs no
  // grant exists — the code alone is not a token — and it is what proves PKCE
  // holds for the code this browser just earned.
  const tokenResp = await page.request.post(`${baseUrl}/token`, {
    form: { grant_type: 'authorization_code', code, code_verifier: VERIFIER },
  });
  if (!tokenResp.ok()) {
    throw new Error(`token exchange failed: ${tokenResp.status()} ${await tokenResp.text()}`);
  }
  const token = await tokenResp.json();
  if (!token.access_token) throw new Error('token exchange returned no access_token');
  log(`Token minted for identity ${token.identity_id}`);

  // A wrong verifier must not buy a token for a replayed code.
  const replay = await page.request.post(`${baseUrl}/token`, {
    form: { grant_type: 'authorization_code', code, code_verifier: 'wrong-verifier' },
  });
  if (replay.ok()) throw new Error('a spent authorization code must not mint a second token');

  // The grant now exists. The admin console should say which account it acts as
  // and which workspace it is pinned to — the two facts that make "which key can
  // reach what" answerable.
  await login(page, baseUrl);
  await page.goto(`${baseUrl}/#/admin`, { waitUntil: 'networkidle' });
  await page.locator('.admin').waitFor({ state: 'visible', timeout: 10000 });
  const grantsTable = page.locator('section', { has: page.locator('h2', { hasText: 'OAuth grants' }) });
  await expect(grantsTable).toBeVisible({ timeout: 10000 });
  const row = grantsTable.locator('tbody tr').first();
  const cells = await row.locator('td').allInnerTexts();
  log(`Grant row: ${cells.map((c) => c.trim()).join(' | ')}`);
  if (!cells.some((c) => c.includes('Acme Research'))) {
    throw new Error(`grant should be pinned to Acme Research, got: ${JSON.stringify(cells)}`);
  }
  await page.waitForTimeout(400);
  await snap('04-admin-grants');
});
