// Message composer, attachments, send button
import { capKey, curProv, currentModel, hasCap } from './models.js';
import { S } from './state.js';
import { $, esc, fmtBytes, ic, toast } from './util.js';

// ───────────────────────── composer ─────────────────────────
export function autosize() { const t = $('#input'); t.style.height = 'auto'; t.style.height = Math.min(t.scrollHeight, innerHeight * 0.4) + 'px'; }

export function updateSendBtn() {
  const b = $('#sendBtn');
  const streaming = S.chat && S.streams.has(S.chat.id);
  b.classList.toggle('stop', !!streaming);
  b.innerHTML = streaming ? ic('stop') : ic('up');
  b.title = streaming ? 'Stop (Esc)' : 'Send';
  b.disabled = !streaming && !$('#input').value.trim() && !S.pending.length;
}

export function fileCard(a) {
  if (a.kind === 'folder') {
    const n = a.count ?? a.files?.length ?? 0;
    return `<div class="file-card"><span class="ic">${ic('folder')}</span><div style="min-width:0"><div class="nm">${esc(a.name)}/</div><small>${n} file${n === 1 ? '' : 's'} · ${fmtBytes(a.size)}</small></div></div>`;
  }
  return `<div class="file-card"><span class="ic">${ic('file')}</span><div style="min-width:0"><div class="nm">${esc(a.name)}</div><small>${fmtBytes(a.size)}</small></div></div>`;
}

export function renderPending() {
  $('#attachments').innerHTML = S.pending.map((a, i) => `<div class="att">${a.kind === 'image' ? `<img src="${a.url}" alt="">` : fileCard(a)}
    <button type="button" class="x" data-rm="${i}">${ic('x')}</button></div>`).join('');
  updateSendBtn();
}

export const TEXT_EXT = /\.(txt|md|markdown|c|h|cc|cpp|hpp|go|rs|py|js|mjs|ts|tsx|jsx|json|ya?ml|toml|ini|cfg|conf|sh|bash|zsh|ps1|bat|html?|css|scss|xml|csv|tsv|log|sql|java|kt|rb|php|lua|pl|asm|s|S|mk|cmake|dockerfile|env|diff|patch|gitignore|service|rules)$/i;
export async function addFiles(files) {
  for (const f of files) {
    if (f.type.startsWith('image/')) {
      if (f.size > 20 << 20) { toast(`${f.name}: image too large`, true); continue; }
      const url = await new Promise(r => { const fr = new FileReader(); fr.onload = () => r(fr.result); fr.readAsDataURL(f); });
      const k = capKey(curProv(), currentModel());
      if (currentModel() && S.caps[k] && !hasCap(curProv(), currentModel(), 'vision')) toast(`${currentModel()} may not support images`);
      S.pending.push({ kind: 'image', name: f.name || 'pasted image', size: f.size, mime: f.type, url, data: url.split(',')[1] });
    } else if (f.type === 'application/pdf') {
      toast('PDFs aren\'t supported yet — paste the text instead', true);
    } else {
      if (f.size > 5 << 20) { toast(`${f.name}: text files up to 5 MB`, true); continue; }
      const text = await f.text();
      if (!TEXT_EXT.test(f.name) && !f.type.startsWith('text/') && /\u0000/.test(text.slice(0, 8000))) { toast(`${f.name}: binary files aren't supported`, true); continue; }
      S.pending.push({ kind: 'file', name: f.name, size: f.size, text });
    }
  }
  renderPending();
}

// A folder arrives as one attachment: text-like files only, junk and secrets skipped.
const FOLDER_MAX_FILES = 60, FOLDER_MAX_ONE = 64 << 10, FOLDER_MAX_TOTAL = 160 << 10;
export const FOLDER_SKIP = /(^|\/)(\.git|\.hg|\.svn|node_modules|bower_components|vendor|dist|build|out|target|bin|obj|__pycache__|\.venv|venv|\.idea|\.vscode|coverage|\.next|\.nuxt|\.cache|Pods|DerivedData)(\/|$)/i;
export const FOLDER_SECRET = /(^|\/)(\.env[^/]*|\.npmrc|\.netrc|id_(rsa|dsa|ecdsa|ed25519)[^/]*|[^/]*\.(pem|key|p12|pfx|jks|keystore|secret|kdbx))$/i;
export const FOLDER_KEEP_DOT = /(^|\/)(\.gitignore|\.dockerignore|\.editorconfig|\.gitattributes)$/i;

const entryFile = entry => new Promise(res => entry.file(res, () => res(null)));

async function walkEntry(entry, rel, out) {
  if (out.length > 4000) return;
  if (entry.isFile) { const f = await entryFile(entry); if (f) out.push({ file: f, rel }); return; }
  if (!entry.isDirectory) return;
  const reader = entry.createReader();
  for (;;) {
    const batch = await new Promise(res => reader.readEntries(res, () => res([])));
    if (!batch.length) break;
    for (const child of batch) await walkEntry(child, rel + '/' + child.name, out);
  }
}

export async function dropAttachments(dt) {
  const entries = [...(dt.items || [])].filter(i => i.kind === 'file').map(i => i.webkitGetAsEntry?.()).filter(Boolean);
  if (!entries.length) { if (dt.files?.length) addFiles([...dt.files]); return; }
  const dirs = entries.filter(en => en.isDirectory);
  if (dirs.length) {
    const pairs = [];
    for (const en of dirs) await walkEntry(en, en.name, pairs);
    await addFolders(pairs);
  }
  const plain = entries.filter(en => en.isFile);
  if (plain.length) addFiles((await Promise.all(plain.map(entryFile))).filter(Boolean));
}

export async function addFolders(pairs) {
  if (!pairs.length) return;
  const groups = new Map();
  for (const p of pairs) {
    const rel = p.rel || p.file.name;
    const root = rel.includes('/') ? rel.split('/')[0] : 'Files';
    if (!groups.has(root)) groups.set(root, []);
    groups.get(root).push({ file: p.file, rel });
  }
  for (const [root, list] of groups) await addFolder(root, list);
  renderPending();
}

async function addFolder(root, list) {
  const files = [];
  let bytes = 0, skipped = 0;
  for (const { file, rel } of list) {
    const path = rel.startsWith(root + '/') ? rel.slice(root.length + 1) : rel;
    const base = path.split('/').pop() || '';
    if (!path || FOLDER_SKIP.test(rel) || FOLDER_SECRET.test(rel)) { skipped++; continue; }
    if (base.startsWith('.') && !FOLDER_KEEP_DOT.test(path)) { skipped++; continue; }
    if (file.type.startsWith('image/') || file.size > FOLDER_MAX_ONE) { skipped++; continue; }
    if (!TEXT_EXT.test(file.name) && !/^(Dockerfile|Makefile|Procfile|LICENSE|README)$/i.test(file.name)) { skipped++; continue; }
    if (files.length >= FOLDER_MAX_FILES || bytes >= FOLDER_MAX_TOTAL) { skipped++; continue; }
    let text;
    try { text = await file.text(); } catch { skipped++; continue; }
    if (/\u0000/.test(text.slice(0, 8000)) || bytes + text.length > FOLDER_MAX_TOTAL) { skipped++; continue; }
    files.push({ path, size: file.size, text });
    bytes += text.length;
  }
  if (!files.length) { toast(`No text files found in ${root}`, true); return; }
  S.pending.push({ kind: 'folder', name: root, size: bytes, count: files.length, files });
  toast(`${root}: ${files.length} file${files.length === 1 ? '' : 's'} attached (${fmtBytes(bytes)})${skipped ? `, ${skipped} skipped` : ''}`);
}
