// MCP tools: servers, approval cards, running calls
import { saveChat } from './chats.js';
import { refreshMsg } from './compare.js';
import { curProv } from './models.js';
import { provById } from './providers.js';
import { scrollToBottom } from './render.js';
import { S, setSetting } from './state.js';
import { $, api, ask, esc, ic, toast } from './util.js';

// ───────────────────────── tools (MCP) ─────────────────────────
const TOOL_KINDS = ['ollama', 'llamacpp', 'openai-compatible'];
const toolsSupported = () => { const p = provById(curProv()); return !!p && p.local && TOOL_KINDS.includes(p.kind); };
export const toolsActive = () => S.settings.toolsOn && toolsSupported() && S.mcp.servers.some(s => s.enabled);
const srvName = id => S.mcp.servers.find(s => s.id === id)?.name || id;
// "Always allow" grants are per tool AND tied to the server's current command line (its fingerprint),
// so reconfiguring a server revokes them. A server's own readOnlyHint is only a label, never a permission.
const allowKey = (server, tool) => { const s = S.mcp.servers.find(x => x.id === server); return s ? `${server}|${s.fp}|${tool}` : ''; };
const isAllowed = c => { const k = allowKey(c.server, c.tool); return !!k && (S.settings.toolsAllow || []).includes(k); };
function pruneAllow() {
  const keep = (S.settings.toolsAllow || []).filter(k => { const [id, fp] = k.split('|'); return S.mcp.servers.some(s => s.id === id && s.fp === fp); });
  if (keep.length !== (S.settings.toolsAllow || []).length) setSetting('toolsAllow', keep);
}

export async function loadMcp() {
  try { const r = await api('/mcp'); S.mcp.servers = r.servers || []; S.mcp.locked = !!r.locked; } catch { S.mcp.servers = []; }
  updateToolsUI();
}
export function updateToolsUI() {
  const b = $('#toolsToggle'); if (!b) return;
  b.classList.toggle('hidden', !toolsSupported() || S.mcp.locked);
  b.classList.toggle('on', toolsActive());
}
export function toolCard(m, c) {
  const waiting = S.mcp.waiters.has(m.id + c.id);
  const st = c.status === 'pending' && !waiting ? 'interrupted' : c.status;
  const label = { pending: 'Waiting for your OK', running: 'Running…', done: 'Done', error: 'Failed', denied: 'Denied', interrupted: 'Not run' }[st];
  let args = ''; try { args = JSON.stringify(c.args, null, 2); } catch { args = String(c.args); }
  return `<div class="tool-card ${esc(st)}"><div class="tool-head">${ic('zap')}<b>${esc(c.tool)}</b><span class="tool-srv">${esc(srvName(c.server))}</span>${c.readOnly ? '<span class="tool-ro" title="The server says this tool only reads. That is its own claim — Guanaco does not rely on it.">says read-only</span>' : ''}<span class="tool-st">${label || ''}</span></div>
    <pre class="tool-args">${esc(args.length > 1500 ? args.slice(0, 1500) + '…' : args)}</pre>
    ${waiting ? `<div class="tool-actions"><button class="btn primary sm" data-act="tool-allow" data-cid="${esc(c.id)}">Run</button><button class="btn sm" data-act="tool-always" data-cid="${esc(c.id)}" title="Skip this question for this tool from now on (until you remove it in Tools)">Run &amp; always allow</button><button class="btn sm" data-act="tool-deny" data-cid="${esc(c.id)}">Deny</button></div>` : ''}
    ${typeof c.result === 'string' && c.result ? `<details class="tool-res"><summary>Result</summary><pre>${esc(c.result.length > 4000 ? c.result.slice(0, 4000) + '\n…' : c.result)}</pre></details>` : ''}</div>`;
}
export function resolveTool(msgId, cid, decision) { const w = S.mcp.waiters.get(msgId + cid); if (w) w(decision); }
export function denyPending() { for (const w of [...S.mcp.waiters.values()]) w('deny'); }
export async function runToolCalls(chat, am, isStopped) {
  await loadMcp(); // fresh fingerprints, so a reconfigured server can't ride on an old "always allow"
  for (const c of am.toolCalls.filter(x => x.status === 'pending')) {
    if (isStopped()) { c.status = 'denied'; c.result = 'Stopped by the user.'; continue; }
    if (!c.known || !c.server) { c.status = 'error'; c.result = `There is no tool named "${c.ns}". Use only the tools you were given.`; refreshMsg(am); continue; }
    let decision = 'allow';
    if (!isAllowed(c)) {
      decision = await new Promise(res => {
        S.mcp.waiters.set(am.id + c.id, d => { S.mcp.waiters.delete(am.id + c.id); res(d); });
        refreshMsg(am); if (S.chat === chat) scrollToBottom();
      });
    }
    if (decision === 'always') {
      const k = allowKey(c.server, c.tool);
      if (k && !(S.settings.toolsAllow || []).includes(k)) setSetting('toolsAllow', [...(S.settings.toolsAllow || []), k]);
    } else if (decision !== 'allow') { c.status = 'denied'; c.result = 'The user denied this tool call.'; refreshMsg(am); continue; }
    c.status = 'running'; refreshMsg(am);
    try {
      const r = await api('/mcp/call', { method: 'POST', body: JSON.stringify({ server: c.server, tool: c.tool, args: c.args }) });
      c.result = r.text || '(no output)'; c.status = r.is_error ? 'error' : 'done';
    } catch (e) { c.result = 'Tool call failed: ' + e.message; c.status = 'error'; }
    refreshMsg(am);
  }
  saveChat(chat);
}

// command-line style splitting (no backslash escapes, so Windows paths survive)
function splitCmd(s) {
  const out = []; let cur = '', q = null, has = false;
  for (const ch of s) {
    if (q) { if (ch === q) q = null; else cur += ch; }
    else if (ch === '"' || ch === "'") { q = ch; has = true; }
    else if (/\s/.test(ch)) { if (cur || has) { out.push(cur); cur = ''; has = false; } }
    else cur += ch;
  }
  if (cur || has) out.push(cur);
  return out;
}
const joinCmd = c => [c.command, ...(c.args || [])].map(a => (a === '' || /[\s"']/.test(a)) ? `"${a}"` : a).join(' ');
const TOOL_PRESETS = [
  { name: 'Files', cmd: 'npx -y @modelcontextprotocol/server-filesystem C:\\Users\\you\\Documents', hint: 'Read and search files in ONE folder you name' },
  { name: 'Fetch', cmd: 'uvx mcp-server-fetch', hint: 'Fetch web pages (uses the network)' },
  { name: 'Memory', cmd: 'npx -y @modelcontextprotocol/server-memory', hint: 'A small notebook the model can write to' },
];
function tsMsg(t, err) { const m = $('#tsMsg'); m.textContent = t || ''; m.classList.toggle('err', !!err); }
function tsClear() { S.mcp.edit = null; $('#tsName').value = ''; $('#tsCmd').value = ''; $('#tsEnv').value = ''; $('#tsFormTitle').textContent = 'Add a tool server'; $('#tsSave').textContent = 'Save server'; }
function renderTools() {
  pruneAllow();
  const al = S.settings.toolsAllow || [];
  $('#toolsAllowList').innerHTML = al.length ? al.map((k, i) => { const [id, , ...t] = k.split('|'); return `<div class="pl-row"><div class="pl-txt"><b>${esc(t.join('|'))}</b><span>${esc(srvName(id))}</span></div><button class="btn sm" data-allow-rm="${i}">Ask again</button></div>`; }).join('')
    : '<div class="muted small">None. Every tool call asks first.</div>';
  $('#tsPresets').innerHTML = TOOL_PRESETS.map((p, i) => `<button type="button" class="pull-chip" data-preset="${i}" title="${esc(p.hint)}">${esc(p.name)}</button>`).join('');
  $('#toolList').innerHTML = S.mcp.servers.length ? S.mcp.servers.map(s => `<div class="pl-row ts-row" data-id="${esc(s.id)}"><div class="pl-txt"><b>${esc(s.name)} <span class="ts-st ${esc(s.status)}">${s.status === 'running' ? s.tools.length + ' tool' + (s.tools.length === 1 ? '' : 's') : s.status}</span></b><span>${esc(joinCmd(s))}</span>${s.error ? `<span class="ts-err">${esc(s.error.slice(0, 220))}</span>` : ''}</div>
      <button class="btn sm" data-ts="start">${s.status === 'running' ? 'Restart' : 'Start'}</button>
      <input type="checkbox" class="switch" data-ts="on" title="Offer these tools to the model" ${s.enabled ? 'checked' : ''}>
      <button class="icon-btn sm" data-ts="edit" title="Edit">${ic('edit')}</button><button class="icon-btn sm" data-ts="del" title="Remove">${ic('trash')}</button></div>`).join('')
    : '<div class="muted small">No tool servers yet. Pick an example below, adjust it, and save.</div>';
}
async function openTools() { $('#toolsModal').classList.remove('hidden'); tsMsg(''); await loadMcp(); renderTools(); }
export function bindTools() {
  $('#toolsToggle').onclick = () => {
    if (!S.settings.toolsOn && !S.mcp.servers.some(s => s.enabled)) { toast('Add a tool server first'); openTools(); return; }
    setSetting('toolsOn', !S.settings.toolsOn); updateToolsUI();
  };
  $('#manageTools').onclick = () => { $('#prefsModal').classList.add('hidden'); openTools(); };
  $('#toolsAllowList').onclick = e => {
    const i = e.target.closest('[data-allow-rm]')?.dataset.allowRm; if (i == null) return;
    setSetting('toolsAllow', (S.settings.toolsAllow || []).filter((_, j) => j !== +i)); renderTools();
  };
  $('#tsPresets').onclick = e => { const p = TOOL_PRESETS[e.target.closest('[data-preset]')?.dataset.preset]; if (p) { $('#tsName').value = p.name; $('#tsCmd').value = p.cmd; $('#tsCmd').focus(); } };
  $('#tsClear').onclick = tsClear;
  $('#tsSave').onclick = async () => {
    const parts = splitCmd($('#tsCmd').value.trim()), name = $('#tsName').value.trim();
    if (!name || !parts.length) { tsMsg('A name and a command are required', true); return; }
    const env = {};
    for (const line of $('#tsEnv').value.split('\n')) { const i = line.indexOf('='); if (i > 0) env[line.slice(0, i).trim()] = line.slice(i + 1).trim(); }
    const ex = S.mcp.edit && S.mcp.servers.find(s => s.id === S.mcp.edit);
    try {
      await api('/mcp/servers', { method: 'POST', body: JSON.stringify({ id: ex?.id || '', name, command: parts[0], args: parts.slice(1), env, enabled: ex ? ex.enabled : true }) });
      tsClear(); tsMsg('Saved. Press Start to check that it works.'); await loadMcp(); renderTools();
    } catch (e) { tsMsg(e.message, true); }
  };
  $('#toolList').onclick = async e => {
    const b = e.target.closest('[data-ts]'); if (!b) return;
    const id = b.closest('.ts-row').dataset.id, s = S.mcp.servers.find(x => x.id === id); if (!s) return;
    try {
      if (b.dataset.ts === 'start') {
        b.disabled = true; b.textContent = 'Starting…'; tsMsg(`Starting ${s.name}… (the first run of npx/uvx can take a minute)`);
        await api(`/mcp/servers/${id}/stop`, { method: 'POST' }).catch(() => { });
        try { await api(`/mcp/servers/${id}/start`, { method: 'POST' }); tsMsg(`${s.name} is running.`); } catch (err) { tsMsg(err.message, true); }
      } else if (b.dataset.ts === 'on') {
        await api('/mcp/servers', { method: 'POST', body: JSON.stringify({ id, name: s.name, command: s.command, args: s.args, env: s.env, enabled: b.checked }) });
      } else if (b.dataset.ts === 'edit') {
        S.mcp.edit = id; $('#tsName').value = s.name; $('#tsCmd').value = joinCmd(s);
        $('#tsEnv').value = Object.entries(s.env || {}).map(([k, v]) => `${k}=${v}`).join('\n');
        $('#tsFormTitle').textContent = 'Edit tool server'; $('#tsSave').textContent = 'Save changes'; $('#tsName').focus(); return;
      } else if (b.dataset.ts === 'del') {
        if (!await ask({ title: 'Remove tool server?', msg: `${s.name} will be stopped and removed.`, input: false, okText: 'Remove', danger: true })) return;
        await api('/mcp/servers/' + id, { method: 'DELETE' });
      }
    } catch (err) { tsMsg(err.message, true); }
    await loadMcp(); renderTools();
  };
}
