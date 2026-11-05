// System prompt panel and assistant profiles
import { saveChat } from './chats.js';
import { curProv, currentModel, findModel, selectModel } from './models.js';
import { isLocal } from './providers.js';
import { S, saveSettings } from './state.js';
import { $, esc, toast, uid } from './util.js';

// ───────────────────────── system prompt panel ─────────────────────────
export const presetById = id => S.settings.presets.find(p => p.id === id);
export function sysTarget() { return S.chat || S.draft; }
export function renderSystemPanel() {
  const t = sysTarget();
  const sel = $('#presetSelect');
  sel.innerHTML = S.settings.presets.map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join('') + `<option value="custom">Custom</option>`;
  sel.value = presetById(t.presetId) ? t.presetId : 'custom';
  $('#systemPrompt').value = t.system || '';
  $('#presetDel').disabled = !presetById(sel.value) || sel.value === 'default';
  $('#presetEdit').disabled = $('#presetDel').disabled;
  const p = presetById(t.presetId), bits = [];
  if (p?.model) bits.push(`model ${p.model.id}`);
  if (p?.temperature != null) bits.push(`temperature ${p.temperature}`);
  $('#profileInfo').textContent = bits.length ? 'Profile also sets: ' + bits.join(' · ') : '';
  $('#profileInfo').classList.toggle('hidden', !bits.length);
}

// A profile = a preset plus optional defaults. The model is only auto-selected if it is local (never silently go to the cloud).
export function applyProfile(p) {
  if (!p) return;
  if (p.model && isLocal(p.model.provider) && findModel(p.model.provider, p.model.id)) {
    if (!(S.streams.size && S.chat && S.streams.has(S.chat.id)) && (p.model.provider !== curProv() || p.model.id !== currentModel())) selectModel(p.model.provider, p.model.id);
  } else if (p.model) toast(`${p.model.id} isn't available — keeping the current model`);
}
export function openProfile(id) {
  const p = id ? presetById(id) : null, t = sysTarget();
  S.profileEdit = p ? p.id : null;
  $('#pfTitle').textContent = p ? 'Edit profile' : 'New profile';
  $('#pfName').value = p ? p.name : '';
  $('#pfPrompt').value = p ? p.prompt : ($('#systemPrompt').value || t.system || '');
  const sel = $('#pfModel');
  sel.innerHTML = '<option value="">Keep whatever model is selected</option>' + S.models.filter(m => m.local).map(m => `<option value="${esc(m.provider)}|${esc(m.id)}">${esc(m.name || m.id)}</option>`).join('');
  sel.value = p?.model ? `${p.model.provider}|${p.model.id}` : '';
  $('#pfTemp').value = p?.temperature ?? '';
  $('#profileModal').classList.remove('hidden');
  setTimeout(() => $('#pfName').focus(), 20);
}
export function saveProfile() {
  const name = $('#pfName').value.trim();
  if (!name) { toast('Give the profile a name', true); return; }
  const o = { name, prompt: $('#pfPrompt').value };
  const mv = $('#pfModel').value;
  if (mv) { const i = mv.indexOf('|'); o.model = { provider: mv.slice(0, i), id: mv.slice(i + 1) }; }
  const tv = $('#pfTemp').value.trim();
  if (tv !== '' && !isNaN(tv)) o.temperature = Math.min(2, Math.max(0, Number(tv)));
  const t = sysTarget();
  let p = S.profileEdit && presetById(S.profileEdit);
  if (p) { delete p.model; delete p.temperature; Object.assign(p, o); }
  else { p = { id: uid(), ...o }; S.settings.presets.push(p); }
  t.presetId = p.id; t.system = p.prompt;
  saveSettings(); $('#profileModal').classList.add('hidden');
  renderSystemPanel(); applyProfile(p);
  if (S.chat) saveChat(S.chat, false);
  toast('Profile saved');
}
