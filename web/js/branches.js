// Conversation branching (fork navigation)
import { cleanForSave, saveChat } from './chats.js';
import { openChat } from './nav.js';
import { renderAll, scroller } from './render.js';
import { S } from './state.js';
import { ic, toast, uid } from './util.js';

// ── conversation branches ──
// A "fork" lives on the first message that differs between versions:
//   m.fork = { others: [tail, …], idx }   — the other versions' message tails, and this one's position among all.
export function forkNav(m) {
  const f = m.fork; if (!f) return '';
  const n = (f.others?.length || 0) + 1, idx = Number(f.idx) || 0;
  return `<span class="fork-nav"><button class="icon-btn" data-act="fork-prev" title="Previous version"${idx <= 0 ? ' disabled' : ''}>${ic('chevl')}</button><span class="fork-n">${idx + 1}/${n}</span><button class="icon-btn" data-act="fork-next" title="Next version"${idx >= n - 1 ? ' disabled' : ''}>${ic('chevr')}</button></span>`;
}
// Pull messages[i…] out into a stored alternative; returns the fork record for the first new message.
export function beginFork(chat, i) {
  const m0 = chat.messages[i], f = m0.fork; delete m0.fork;
  const tail = chat.messages.splice(i);
  const tails = f ? f.others.slice() : [];
  if (f) tails.splice(f.idx, 0, tail); else tails.push(tail);
  return { others: tails, idx: tails.length };
}
function forkSwitch(chat, i, j) {
  const m0 = chat.messages[i], f = m0.fork; delete m0.fork;
  const tails = f.others.slice();
  tails.splice(f.idx, 0, chat.messages.slice(i));
  const target = tails.splice(j, 1)[0];
  chat.messages.splice(i, chat.messages.length, ...target);
  chat.messages[i].fork = { others: tails, idx: j };
}
export function forkStep(chat, host, dir) {
  if (S.streams.has(chat.id)) { toast('Wait for the reply to finish'); return; }
  const i = chat.messages.indexOf(host), f = host.fork;
  if (i < 0 || !f) return;
  const j = f.idx + dir;
  if (j < 0 || j > f.others.length) return;
  const st = scroller().scrollTop;
  forkSwitch(chat, i, j);
  renderAll(); scroller().scrollTop = st;
  saveChat(chat, false);
}
export async function branchChat(chat, host) {
  const i = chat.messages.indexOf(host); if (i < 0) return;
  const msgs = JSON.parse(cleanForSave({ messages: chat.messages.slice(0, i + 1) })).messages;
  msgs.forEach(x => delete x.fork);
  const now = Date.now();
  const c = { id: uid(), title: (chat.title && chat.title !== 'New chat' ? chat.title : 'Chat') + ' (branch)', provider: chat.provider, model: chat.model, created: now, updated: now, pinned: false, system: chat.system, presetId: chat.presetId, messages: msgs };
  S.cache.set(c.id, c);
  await saveChat(c, false);
  await openChat(c.id);
  toast('Branched into a new chat');
}
