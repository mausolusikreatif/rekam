const path = require('path');

// PORT and OUTPUT come from run.sh so the config, the host server, and the
// reporter all agree on where to look.
const PORT = process.env.REKAM_TEST_PORT || '3010';
const OUTPUT_DIR = process.env.RESULTS_DIR || path.join(__dirname, 'results', 'latest');

module.exports = {
  testDir: path.join(__dirname, 'tests'),
  testMatch: '**/*.js',
  outputDir: path.join(OUTPUT_DIR, 'test-results'),
  reporter: [
    ['html', { outputFolder: path.join(OUTPUT_DIR, 'playwright-report'), open: 'never' }],
    [path.join(__dirname, 'custom-reporter.js'), { outputDir: OUTPUT_DIR }]
  ],
  use: {
    baseURL: `http://localhost:${PORT}`,
    trace: 'on',
    video: { mode: 'on', size: { width: 1280, height: 800 } },
    screenshot: 'on',
    viewport: { width: 1280, height: 800 },
    // slowMo paces every browser action so the recorded video is watchable —
    // clicks and typing don't blur past. Overridable via REKAM_SLOWMO (ms).
    launchOptions: { slowMo: Number(process.env.REKAM_SLOWMO || 600) }
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
  workers: 1,
  // Generous because slowMo adds a delay to every action.
  timeout: 120000,
  retries: 0
};
