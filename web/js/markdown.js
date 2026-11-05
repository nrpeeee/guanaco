// Markdown rendering and sanitising
import { domainOf, esc, ic } from './util.js';

// ───────────────────────── markdown ─────────────────────────
marked.use({
  gfm: true, breaks: false,
  renderer: {
    code(code, lang) {
      lang = (lang || '').trim().split(/\s+/)[0].toLowerCase();
      let html;
      try {
        html = lang && hljs.getLanguage(lang) ? hljs.highlight(code, { language: lang, ignoreIllegals: true }).value : esc(code);
      } catch { html = esc(code); }
      return `<div class="code-block"><div class="code-head"><span>${esc(lang || 'text')}</span><button class="copy-code" type="button">${ic('copy')}<span>Copy</span></button></div><pre><code class="hljs">${html}</code></pre></div>`;
    },
  },
});
DOMPurify.addHook('afterSanitizeAttributes', n => {
  if (n.tagName === 'A' && n.getAttribute('href')) { n.setAttribute('target', '_blank'); n.setAttribute('rel', 'noopener noreferrer'); }
});
export const renderMD = text => DOMPurify.sanitize(marked.parse(text || ''), { USE_PROFILES: { html: true, svg: true } });

export function splitThink(content) {
  const m = content.match(/^\s*<think>([\s\S]*?)(<\/think>|$)/);
  if (!m) return null;
  return { thinking: m[1].trim(), content: content.slice(m[0].length).trimStart(), open: !m[2] };
}

export function linkCitations(root, sources) {
  if (!sources?.length) return;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: n => n.parentElement.closest('pre,code,a') ? NodeFilter.FILTER_REJECT : (/\[\d/.test(n.nodeValue) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_SKIP),
  });
  const nodes = []; while (walker.nextNode()) nodes.push(walker.currentNode);
  const re = /\[(\d{1,2}(?:\s*[,–-]\s*\d{1,2})*)\]/g;
  for (const node of nodes) {
    const txt = node.nodeValue; let last = 0, m, frag = null;
    re.lastIndex = 0;
    while ((m = re.exec(txt))) {
      const nums = m[1].split(/\s*,\s*/).map(Number).filter(n => n >= 1 && n <= sources.length);
      if (!nums.length) continue;
      frag ??= document.createDocumentFragment();
      frag.append(txt.slice(last, m.index));
      for (const n of nums) {
        const a = document.createElement('a');
        a.className = 'cite'; a.href = sources[n - 1].url; a.target = '_blank'; a.rel = 'noopener noreferrer';
        a.title = `${sources[n - 1].title}\n${domainOf(sources[n - 1].url)}`; a.textContent = n;
        frag.append(a);
      }
      last = m.index + m[0].length;
    }
    if (frag) { frag.append(txt.slice(last)); node.replaceWith(frag); }
  }
}
