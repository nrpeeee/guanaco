// Chat export and import
import { S } from './state.js';
import { toast } from './util.js';

// ───────────────────────── export / import ─────────────────────────
export async function download(path, fallbackName) {
  const r = await fetch('/api' + path, { headers: { 'X-Guanaco': '1' } });
  if (!r.ok) { toast('Export failed', true); return; }
  const cd = r.headers.get('content-disposition') || '';
  const name = (cd.match(/filename="([^"]+)"/) || [])[1] || fallbackName;
  const url = URL.createObjectURL(await r.blob());
  const a = Object.assign(document.createElement('a'), { href: url, download: name });
  document.body.appendChild(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 2000);
}
export async function exportChat(id, fmt) {
  if (S.streams.has(id)) { toast('Wait for the reply to finish'); return; }
  if (fmt === 'copy') {
    const md = await fetch(`/api/chats/${id}/export?format=md`, { headers: { 'X-Guanaco': '1' } }).then(r => r.text());
    await navigator.clipboard.writeText(md); toast('Copied as Markdown'); return;
  }
  const q = fmt === 'md-think' ? 'format=md&thinking=1' : 'format=' + fmt;
  download(`/chats/${id}/export?${q}`, id + '.' + (fmt.startsWith('md') ? 'md' : fmt));
}
