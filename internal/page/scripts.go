package page

// hookJS runs before any page script. It wraps drawImage so canvas viewers,
// which keep no <img> for the content, still reveal what they drew and where.
const hookJS = `(() => {
  if (window.__picpullDraws) return;
  const draws = [];
  Object.defineProperty(window, '__picpullDraws', {value: draws});
  const orig = CanvasRenderingContext2D.prototype.drawImage;
  CanvasRenderingContext2D.prototype.drawImage = function (src, ...args) {
    try {
      const url = src && (src.currentSrc || src.src);
      if (url) {
        // drawImage(img, dx, dy), (img, dx, dy, dw, dh) or
        // (img, sx, sy, sw, sh, dx, dy, dw, dh).
        const full = args.length === 8;
        draws.push({
          url: url,
          canvas: this.canvas,
          dy: (full ? args[5] : args[1]) || 0,
          dh: (full ? args[7] : args.length === 4 ? args[3] : src.naturalHeight) || 0,
          w: src.naturalWidth || 0,
          h: src.naturalHeight || 0,
        });
      }
    } catch (e) {}
    return orig.call(this, src, ...args);
  };
})();`

// scrollJS steps one screen at a time: jumping to the bottom lets
// IntersectionObserver-based lazy loading skip the images in between.
const scrollJS = `(async () => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  const root = document.documentElement;
  let stable = 0;
  for (let i = 0; i < 500 && stable < 3; i++) {
    const before = root.scrollHeight;
    window.scrollBy(0, window.innerHeight);
    await sleep(300);
    const atBottom = window.scrollY + window.innerHeight >= root.scrollHeight - 2;
    stable = atBottom && root.scrollHeight === before ? stable + 1 : 0;
  }
  return true;
})()`

// validSelectorJS is called with the --in selector.
const validSelectorJS = `(sel) => {
  try { document.querySelectorAll(sel); return true; }
  catch (e) { return false; }
}`

// collectJS is called with the --in selector (or "").
const collectJS = `(sel) => {
  let scope = null;
  if (sel) {
    try { scope = Array.from(document.querySelectorAll(sel)); }
    catch (e) { return {invalid: true, items: []}; }
  }
  const inScope = el => !scope || scope.some(s => s.contains(el));
  const sx = window.scrollX, sy = window.scrollY;
  const box = el => {
    const r = el.getBoundingClientRect();
    return {x: r.left + sx, y: r.top + sy, boxH: r.height, positioned: r.width > 0 && r.height > 0};
  };
  const items = [];
  for (const img of document.querySelectorAll('img')) {
    items.push(Object.assign({
      kind: 'img',
      src: img.getAttribute('src') || '',
      currentSrc: img.currentSrc || '',
      srcset: img.getAttribute('srcset') || '',
      dataSrc: img.getAttribute('data-src') || '',
      dataOriginal: img.getAttribute('data-original') || '',
      w: img.naturalWidth || 0,
      h: img.naturalHeight || 0,
      inScope: inScope(img),
    }, box(img)));
  }
  for (const el of document.querySelectorAll('*')) {
    const bg = getComputedStyle(el).backgroundImage;
    if (!bg || bg === 'none') continue;
    for (const m of bg.matchAll(/url\(["']?(.*?)["']?\)/g)) {
      items.push(Object.assign({kind: 'background', src: m[1], inScope: inScope(el)}, box(el)));
    }
  }
  for (const d of (window.__picpullDraws || [])) {
    const r = d.canvas.getBoundingClientRect();
    const scale = d.canvas.height ? r.height / d.canvas.height : 1;
    items.push({
      kind: 'canvas', src: d.url, w: d.w, h: d.h, inScope: inScope(d.canvas),
      x: r.left + sx, y: r.top + sy + d.dy * scale, boxH: d.dh * scale,
      positioned: r.width > 0 && r.height > 0,
    });
  }
  return {invalid: false, items: items};
}`

// markNextJS is called with the --next value. It tries it as a CSS selector
// first and falls back to exact visible text, so "button.next" and "다음 >"
// both work. The first match is marked for the click.
const markNextJS = `(target) => {
  for (const el of document.querySelectorAll('[data-picpull-next]')) el.removeAttribute('data-picpull-next');
  let found = [];
  try { found = Array.from(document.querySelectorAll(target)); } catch (e) {}
  if (found.length === 0) {
    found = Array.from(document.querySelectorAll('a, button'))
      .filter(el => (el.innerText || '').trim() === target);
  }
  if (found.length > 0) found[0].setAttribute('data-picpull-next', '');
  return found.length;
}`

// fetchJS runs in the page so the request carries the page's cookies and
// origin. FileReader avoids building a huge string argument list.
const fetchJS = `(url) => fetch(url, {credentials: 'include'}).then(async r => {
  if (!r.ok) return {status: r.status, type: '', data: ''};
  const blob = await r.blob();
  const data = await new Promise((ok, fail) => {
    const fr = new FileReader();
    fr.onload = () => ok(fr.result);
    fr.onerror = () => fail(fr.error);
    fr.readAsDataURL(blob);
  });
  return {status: r.status, type: r.headers.get('content-type') || blob.type, data: data};
})`
