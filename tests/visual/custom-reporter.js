const fs = require('fs');
const path = require('path');

// A self-contained HTML dashboard of the run: per-test log, video, trace link,
// and named screenshots with a lightbox. Written to <outputDir>/index.html so a
// run's results are a single openable page.
class RekamReporter {
  constructor(options = {}) {
    this.outputDir = options.outputDir || process.env.RESULTS_DIR || '/output';
    this.tests = [];
    this.startTime = Date.now();
  }

  onBegin() {
    console.log(`\n  Rekam Reporter → ${this.outputDir}/index.html`);
  }

  onTestEnd(test, result) {
    const screenshots = result.attachments
      .filter(a => a.contentType === 'image/png' && a.path && a.name !== 'screenshot')
      .map(a => ({ name: a.name, relPath: path.relative(this.outputDir, a.path) }));

    const autoShot = result.attachments.find(
      a => a.contentType === 'image/png' && a.path && a.name === 'screenshot'
    );

    const logsAttach = result.attachments.find(a => a.name === 'logs' && a.path);
    const logs = logsAttach
      ? fs.readFileSync(logsAttach.path, 'utf8').split('\n').filter(Boolean)
      : [];

    const videoAttach = result.attachments.find(a => a.contentType === 'video/webm' && a.path);
    const traceAttach = result.attachments.find(a => a.name === 'trace' && a.path);

    this.tests.push({
      name: test.title,
      passed: result.status === 'passed',
      duration: result.duration,
      error: result.error?.message,
      screenshots,
      autoScreenshot: autoShot ? path.relative(this.outputDir, autoShot.path) : null,
      logs,
      videoRelPath: videoAttach ? path.relative(this.outputDir, videoAttach.path) : null,
      traceRelPath: traceAttach ? path.relative(this.outputDir, traceAttach.path) : null
    });
  }

  onEnd() {
    this.generateHtml();
    const passed = this.tests.filter(t => t.passed).length;
    const failed = this.tests.length - passed;
    console.log(`  Rekam Reporter: ${passed} passed, ${failed} failed\n`);
  }

  generateHtml() {
    const passed = this.tests.filter(t => t.passed).length;
    const failed = this.tests.length - passed;
    const total = this.tests.length;
    const duration = ((Date.now() - this.startTime) / 1000).toFixed(1);
    const timestamp = new Date().toISOString();

    const testCards = this.tests.map(t => {
      const statusClass = t.passed ? 'pass' : 'fail';
      const statusLabel = t.passed ? 'PASS' : 'FAIL';
      const dur = t.duration ? `${(t.duration / 1000).toFixed(1)}s` : '';

      const screenshotGrid = t.screenshots.map(s => `
        <figure class="ss-fig" onclick="openLightbox('${s.relPath}', '${esc(s.name)}')">
          <img src="${s.relPath}" alt="${esc(s.name)}" loading="lazy" />
          <figcaption>${esc(s.name)}</figcaption>
        </figure>`).join('');

      const logsHtml = t.logs.map(l => `<div class="log-line">${esc(l)}</div>`).join('');
      const errorHtml = t.error ? `<div class="error-block">${esc(t.error)}</div>` : '';
      const videoHtml = t.videoRelPath
        ? `<video class="test-video" controls preload="metadata" src="${t.videoRelPath}"></video>`
        : '<div class="no-artifact">No video</div>';
      const traceHtml = t.traceRelPath
        ? `<div class="trace-box"><span class="trace-label">Trace</span><a href="playwright-report/index.html" target="_blank" class="trace-open-btn">Open in Playwright Viewer →</a></div>`
        : '';
      const autoShotHtml = t.autoScreenshot
        ? `<div class="auto-shot"><div class="section-label" style="margin-top:14px">Final State (auto)</div>
            <figure class="ss-fig" onclick="openLightbox('${t.autoScreenshot}', 'final state')"><img src="${t.autoScreenshot}" alt="auto screenshot" loading="lazy" /></figure></div>`
        : '';

      return `
  <section class="test-card ${statusClass}" id="${esc(t.name)}">
    <div class="test-header" onclick="toggleCard('${esc(t.name)}')">
      <span class="badge ${statusClass}">${statusLabel}</span>
      <h2 class="test-title">${esc(t.name)}</h2>
      <span class="test-dur">${dur}</span>
      <span class="chevron">▼</span>
    </div>
    <div class="test-body">
      ${errorHtml}
      <div class="test-columns">
        <div class="test-left">
          <div class="section-label">Log</div>
          <div class="log-block">${logsHtml || '<span class="dim">No log output</span>'}</div>
          ${traceHtml}
          <div class="section-label" style="margin-top:16px">Recording</div>
          ${videoHtml}
          ${autoShotHtml}
        </div>
        <div class="test-right">
          <div class="section-label">Screenshots (${t.screenshots.length})</div>
          <div class="ss-grid">${screenshotGrid || '<span class="dim">No screenshots</span>'}</div>
        </div>
      </div>
    </div>
  </section>`;
    }).join('');

    const html = `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8" /><meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>Rekam Visual Tests — ${timestamp}</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: 'Segoe UI', system-ui, sans-serif; background: #14110c; color: #d8cfc0; font-size: 13px; line-height: 1.5; }
  .top-bar { background: #1c1811; border-bottom: 1px solid #372e1f; padding: 18px 32px; display: flex; align-items: center; gap: 20px; position: sticky; top: 0; z-index: 100; }
  .top-bar h1 { font-size: 17px; font-weight: 700; color: #efe7d8; }
  .top-bar .ts { font-size: 11px; color: #6b5f4a; margin-top: 2px; }
  .top-bar .dur { font-size: 11px; color: #8a7c62; }
  .pills { display: flex; gap: 8px; margin-left: auto; align-items: center; }
  .pill { border-radius: 20px; padding: 3px 12px; font-size: 11px; font-weight: 700; }
  .pill.total { background: #372e1f; color: #d8cfc0; }
  .pill.pass  { background: rgba(76,140,74,.18); color: #7cc07a; border: 1px solid rgba(76,140,74,.35); }
  .pill.fail  { background: rgba(180,80,60,.18); color: #e08a76; border: 1px solid rgba(180,80,60,.35); }
  .pw-link { margin-left: 4px; font-size: 11px; font-weight: 600; color: #d69a7a; background: rgba(176,80,60,.12); border: 1px solid rgba(176,80,60,.3); border-radius: 5px; padding: 3px 10px; text-decoration: none; white-space: nowrap; }
  main { max-width: 1440px; margin: 0 auto; padding: 24px 32px; display: flex; flex-direction: column; gap: 12px; }
  .test-card { border-radius: 10px; border: 1px solid #372e1f; overflow: hidden; background: #1a160f; }
  .test-card.pass { border-left: 3px solid #7cc07a; }
  .test-card.fail { border-left: 3px solid #e08a76; }
  .test-card.collapsed .test-body { display: none; }
  .test-card.collapsed .chevron { transform: rotate(-90deg); }
  .test-header { display: flex; align-items: center; gap: 12px; padding: 13px 20px; cursor: pointer; user-select: none; background: #211c13; transition: background .15s; }
  .test-header:hover { background: #272117; }
  .badge { font-size: 10px; font-weight: 800; letter-spacing: .05em; padding: 2px 8px; border-radius: 4px; flex-shrink: 0; }
  .badge.pass { background: rgba(76,140,74,.18); color: #7cc07a; }
  .badge.fail { background: rgba(180,80,60,.18); color: #e08a76; }
  .test-title { font-size: 13px; font-weight: 600; color: #efe7d8; flex: 1; }
  .test-dur { font-size: 11px; color: #6b5f4a; }
  .chevron { color: #8a7c62; font-size: 11px; transition: transform .2s; }
  .test-body { padding: 18px 20px; }
  .error-block { background: rgba(180,80,60,.1); border: 1px solid rgba(180,80,60,.3); border-radius: 6px; padding: 10px 14px; color: #e08a76; font-size: 12px; margin-bottom: 14px; font-family: monospace; white-space: pre-wrap; }
  .test-columns { display: flex; gap: 20px; }
  .test-left { width: 320px; flex-shrink: 0; }
  .test-right { flex: 1; min-width: 0; }
  .section-label { font-size: 10px; font-weight: 700; letter-spacing: .08em; color: #6b5f4a; text-transform: uppercase; margin-bottom: 6px; }
  .log-block { background: #14110c; border: 1px solid #372e1f; border-radius: 6px; padding: 9px 11px; max-height: 200px; overflow-y: auto; }
  .log-line { color: #b6ab96; font-size: 11.5px; padding: 1px 0; font-family: 'Fira Code', monospace; }
  .dim { color: #4a4030; font-style: italic; }
  .trace-box { margin-top: 10px; background: rgba(176,80,60,.07); border: 1px solid rgba(176,80,60,.22); border-radius: 6px; padding: 8px 11px; display: flex; flex-direction: column; gap: 5px; }
  .trace-label { font-size: 10px; font-weight: 700; color: #d69a7a; text-transform: uppercase; letter-spacing: .06em; }
  .trace-open-btn { font-size: 11px; font-weight: 600; color: #d69a7a; text-decoration: none; }
  .test-video { width: 100%; border-radius: 6px; border: 1px solid #372e1f; background: #000; margin-top: 8px; max-height: 180px; }
  .no-artifact { color: #4a4030; font-style: italic; padding: 6px 0; }
  .ss-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 8px; }
  .ss-fig { cursor: zoom-in; background: #14110c; border: 1px solid #372e1f; border-radius: 6px; overflow: hidden; transition: border-color .15s, transform .15s; }
  .ss-fig:hover { border-color: #b4503c; transform: translateY(-2px); }
  .ss-fig img { width: 100%; height: 130px; object-fit: cover; object-position: top; display: block; }
  .ss-fig figcaption { font-size: 10px; color: #6b5f4a; padding: 4px 7px; font-family: 'Fira Code', monospace; }
  .lightbox { display: none; position: fixed; inset: 0; background: rgba(0,0,0,.92); z-index: 9999; align-items: center; justify-content: center; cursor: zoom-out; flex-direction: column; gap: 12px; }
  .lightbox.open { display: flex; }
  .lightbox img { max-width: 96vw; max-height: 88vh; border-radius: 8px; }
  .lightbox-cap { color: #d8cfc0; font-size: 12px; background: rgba(0,0,0,.6); padding: 4px 12px; border-radius: 20px; }
  @media (max-width: 860px) { .test-columns { flex-direction: column; } .test-left { width: 100%; } }
</style></head>
<body>
  <div class="top-bar">
    <div><h1>Rekam Visual Tests</h1><div class="ts">${timestamp}</div></div>
    <span class="dur">⏱ ${duration}s</span>
    <div class="pills">
      <span class="pill total">${total} tests</span>
      <span class="pill pass">${passed} passed</span>
      ${failed > 0 ? `<span class="pill fail">${failed} failed</span>` : ''}
    </div>
    <a class="pw-link" href="playwright-report/index.html" target="_blank">Playwright Viewer ↗</a>
  </div>
  <main>${testCards}</main>
  <div class="lightbox" id="lb" onclick="closeLb()"><img id="lb-img" src="" alt="" /><div class="lightbox-cap" id="lb-cap"></div></div>
  <script>
    document.querySelectorAll('.test-card.pass').forEach(c => c.classList.add('collapsed'));
    function toggleCard(name) { document.getElementById(name).classList.toggle('collapsed'); }
    function openLightbox(src, cap) { event.stopPropagation(); document.getElementById('lb-img').src = src; document.getElementById('lb-cap').textContent = cap || src.split('/').pop(); document.getElementById('lb').classList.add('open'); }
    function closeLb() { document.getElementById('lb').classList.remove('open'); }
    document.addEventListener('keydown', e => { if (e.key === 'Escape') closeLb(); });
  </script>
</body></html>`;

    fs.writeFileSync(path.join(this.outputDir, 'index.html'), html);
  }
}

function esc(str) {
  if (!str) return '';
  return String(str).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

module.exports = RekamReporter;
