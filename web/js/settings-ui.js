// Preferences dialog bindings
import { updateModelUI } from './models.js';
import { greeting } from './render.js';
import { DEFAULT_PARAMS, S, saveSettings, setSetting } from './state.js';
import { $, $$, toast } from './util.js';

// ───────────────────────── settings binding ─────────────────────────
export function applyTheme() {
  const t = S.settings.theme;
  if (t === 'system') delete document.documentElement.dataset.theme; else document.documentElement.dataset.theme = t;
  try { localStorage.setItem('Guanaco.theme', JSON.stringify(t)); } catch { }
  document.documentElement.dataset.font = S.settings.font;
  $$('#themeSel button').forEach(b => b.classList.toggle('on', b.dataset.v === t));
  $$('#fontSel button').forEach(b => b.classList.toggle('on', b.dataset.v === S.settings.font));
}

export function bindSettings() {
  const p = S.settings.params;
  const sliders = [['temperature', 'tempOut', 'temperature'], ['topP', 'topPOut', 'top_p'], ['topK', 'topKOut', 'top_k'], ['repeatPenalty', 'repeatOut', 'repeat_penalty']];
  for (const [id, out, key] of sliders) {
    const el = $('#' + id); el.value = p[key]; $('#' + out).textContent = p[key];
    el.oninput = () => { p[key] = Number(el.value); $('#' + out).textContent = el.value; saveSettings(); };
  }
  $('#numCtx').value = String(p.num_ctx || 0);
  $('#numCtx').onchange = e => { p.num_ctx = Number(e.target.value); saveSettings(); };
  $('#numPredict').value = p.num_predict; $('#numPredict').oninput = e => { p.num_predict = e.target.value; saveSettings(); };
  $('#seed').value = p.seed; $('#seed').oninput = e => { p.seed = e.target.value; saveSettings(); };
  $('#keepAlive').value = S.settings.keepAlive; $('#keepAlive').onchange = e => setSetting('keepAlive', e.target.value);
  $('#resetParams').onclick = () => { S.settings.params = { ...DEFAULT_PARAMS }; saveSettings(); bindSettings(); toast('Parameters reset'); };

  const tm = () => $$('#thinkMode button').forEach(b => b.classList.toggle('on', b.dataset.v === S.settings.thinkMode));
  tm(); $('#thinkMode').onclick = e => { const v = e.target.closest('button')?.dataset.v; if (v) { setSetting('thinkMode', v); tm(); updateModelUI(); } };
  $('#showThinking').checked = S.settings.showThinking; $('#showThinking').onchange = e => setSetting('showThinking', e.target.checked);

  $('#searchRewrite').checked = S.settings.searchRewrite; $('#searchRewrite').onchange = e => setSetting('searchRewrite', e.target.checked);
  $('#searchResults').value = String(S.settings.searchResults); $('#searchResults').onchange = e => setSetting('searchResults', Number(e.target.value));
  $('#searchEngine').value = S.settings.searchEngine;
  $('#searxUrl').classList.toggle('hidden', S.settings.searchEngine !== 'searxng');
  $('#searchEngine').onchange = e => { setSetting('searchEngine', e.target.value); $('#searxUrl').classList.toggle('hidden', e.target.value !== 'searxng'); };
  $('#searxUrl').value = S.settings.searxUrl; $('#searxUrl').oninput = e => setSetting('searxUrl', e.target.value.trim());
  $('#searchToggle').classList.toggle('on', S.settings.searchOn);
  $('#ragCloud').checked = !!S.settings.ragCloud; $('#ragCloud').onchange = e => setSetting('ragCloud', e.target.checked);

  $('#userName').value = S.settings.userName; $('#userName').oninput = e => { setSetting('userName', e.target.value); $('#greeting').innerHTML = greeting(); };
  $('#autoTitle').checked = S.settings.autoTitle; $('#autoTitle').onchange = e => setSetting('autoTitle', e.target.checked);
  $('#enterSends').checked = S.settings.enterSends; $('#enterSends').onchange = e => setSetting('enterSends', e.target.checked);
  applyTheme();
}
