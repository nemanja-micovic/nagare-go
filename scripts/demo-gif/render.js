// Render recorded frames to PNGs with one browser page.
const { chromium } = require('playwright');
const fs = require('fs');
(async () => {
  const data = JSON.parse(fs.readFileSync(process.argv[2]));
  const out = process.argv[3];
  fs.mkdirSync(out, { recursive: true });
  const b = await chromium.launch({ ...(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {}) });
  const cw = 8, ch = 17;  // cell size at 13px DejaVu Sans Mono
  const p = await b.newPage({ viewport: { width: data.W * cw + 24, height: data.H * ch + 24 }, deviceScaleFactor: 1 });
  const css = '<style>body{margin:0;background:#1a1b26}.t{font-family:"DejaVu Sans Mono",monospace;font-size:13px;margin:12px;white-space:pre}.r{height:17px;line-height:17px;overflow:hidden}.r span{display:inline-block;height:17px}</style>';
  for (let i = 0; i < data.frames.length; i++) {
    await p.setContent('<html><head>' + css + '</head><body><div class="t">' + data.frames[i][0] + '</div></body></html>');
    await p.screenshot({ path: `${out}/${String(i).padStart(4, '0')}.png` });
  }
  fs.writeFileSync(`${out}/durations.json`, JSON.stringify(data.frames.map(f => f[1])));
  await b.close();
})();
