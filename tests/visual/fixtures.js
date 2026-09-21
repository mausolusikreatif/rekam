const { test: base } = require('@playwright/test');

const PORT = process.env.REKAM_TEST_PORT || '3010';

// Runs in the page on every navigation. Draws a visible cursor that follows the
// real mouse and pulses on click, so the recorded video/screenshots show *where*
// each interaction happens instead of elements "magically" reacting. The colour
// is rekam's ink accent so the cursor reads as part of the product.
function mouseHelper() {
  const attach = () => {
    if (document.getElementById('pw-cursor')) return;
    const style = document.createElement('style');
    style.textContent = `
      #pw-cursor {
        position: fixed; top: 0; left: 0; width: 20px; height: 20px;
        margin: -10px 0 0 -10px; border-radius: 50%;
        background: rgba(176,80,60,.30); border: 2px solid #b4503c;
        box-shadow: 0 0 8px rgba(176,80,60,.5);
        pointer-events: none; z-index: 2147483647;
        transition: width .1s, height .1s, margin .1s, background .1s;
      }
      #pw-cursor.click {
        width: 34px; height: 34px; margin: -17px 0 0 -17px;
        background: rgba(176,80,60,.15);
      }`;
    const dot = document.createElement('div');
    dot.id = 'pw-cursor';
    document.head.appendChild(style);
    document.body.appendChild(dot);
    document.addEventListener('mousemove', (e) => {
      dot.style.left = e.clientX + 'px';
      dot.style.top = e.clientY + 'px';
    }, true);
    document.addEventListener('mousedown', () => dot.classList.add('click'), true);
    document.addEventListener('mouseup', () => dot.classList.remove('click'), true);
  };
  if (document.body) attach();
  else window.addEventListener('DOMContentLoaded', attach);
}

exports.test = base.extend({
  baseUrl: [async ({}, use) => {
    await use(`http://localhost:${PORT}`);
  }, { scope: 'test' }],

  // The marketing site is its own origin, as it is in production: static
  // pages at the site root, with the app behind /ui/. A test that wants the
  // landing page or the legal documents asks for this, not baseUrl.
  marketingUrl: [async ({}, use) => {
    await use(`http://localhost:${process.env.REKAM_MARKETING_PORT || 3011}`);
  }, { scope: 'test' }],

  // Override the built-in page to inject the visible cursor on every load.
  page: async ({ page }, use) => {
    await page.addInitScript(mouseHelper);
    await use(page);
  },

  // snap(name) takes a named screenshot into the test's output dir and attaches
  // it, so the custom reporter can lay them out per step.
  snap: [async ({ page }, use, testInfo) => {
    await use(async (name) => {
      const file = testInfo.outputPath(`${name}.png`);
      await page.screenshot({ path: file });
      await testInfo.attach(name, { path: file, contentType: 'image/png' });
    });
  }, { scope: 'test' }],

  log: [async ({}, use, testInfo) => {
    const entries = [];
    await use((msg) => {
      entries.push(msg);
      console.log(`  ${msg}`);
    });
    if (entries.length > 0) {
      await testInfo.attach('logs', {
        body: Buffer.from(entries.join('\n')),
        contentType: 'text/plain'
      });
    }
  }, { scope: 'test' }]
});
