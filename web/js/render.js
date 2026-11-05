// Message rendering and scrolling
import { renderChatList } from './chats.js';
import { buildCompare, renderAssistant, renderUser } from './compare.js';
import { updateSendBtn } from './composer.js';
import { S } from './state.js';
import { renderSystemPanel } from './system-prompt.js';
import { $, esc } from './util.js';

// ───────────────────────── rendering ─────────────────────────
export function greeting() {
  const h = new Date().getHours(), n = S.settings.userName?.trim();
  const base = h < 5 ? 'Up late' : h < 12 ? 'Good morning' : h < 17 ? 'Good afternoon' : 'Good evening';
  return n ? `${esc(base)}, <span class="grad">${esc(n)}</span>` : esc(base);
}

export function renderTopbar() {
  const c = S.chat;
  $('#chatTitle').textContent = c?.messages.length ? (c.title || 'New chat') : '';
  $('#exportBtn').classList.toggle('hidden', !c?.messages.length);
  document.title = c?.title && c.messages.length ? `${c.title} · Guanaco` : 'Guanaco';
}

export function renderAll() {
  const empty = !S.chat || !S.chat.messages.length;
  $('#app').classList.toggle('empty', empty);
  $('#greeting').innerHTML = greeting();
  $('#input').placeholder = empty ? 'How can I help you today?' : 'Reply…';
  renderTopbar();
  renderMessages();
  updateJump();
  renderChatList();
  renderSystemPanel();
  updateSendBtn();
}

function renderMessages() {
  const box = $('#messages');
  box.innerHTML = '';
  if (!S.chat) return;
  const msgs = S.chat.messages;
  msgs.forEach((m, i) => box.appendChild(buildMsg(m, i === msgs.length - 1)));
}

function buildMsg(m, isLast) {
  const el = document.createElement('div');
  el.className = `msg ${m.role}${isLast ? ' last' : ''}`;
  el.dataset.id = m.id;
  if (m.role === 'user') renderUser(el, m);
  else if (m.cols) buildCompare(el, m);
  else renderAssistant(el, m);
  return el;
}


// ───────────────────────── scrolling ─────────────────────────
export const scroller = () => $('#scroller');
export const isNearBottom = () => { const s = scroller(); return s.scrollHeight - s.scrollTop - s.clientHeight < 120; };
export function scrollToBottom(force) { const s = scroller(); if (force || isNearBottom()) s.scrollTop = s.scrollHeight; }
// The jump-to-bottom button: only in a chat that is scrolled up. Also run on
// every render, because switching to an empty chat fires no scroll event.
export const updateJump = () => $('#jumpBtn').classList.toggle('hidden', isNearBottom() || $('#app').classList.contains('empty'));
