// Read-aloud and dictation
import { autosize, updateSendBtn } from './composer.js';
import { renderMD, splitThink } from './markdown.js';
import { S, setSetting } from './state.js';
import { $, $$, ask, esc, ic, toast } from './util.js';

// ───────────────────────── voice ─────────────────────────
// Read-aloud uses on-device system voices only (browser "online" voices stream text to a vendor, so they're not offered).
// Dictation goes to a local whisper server, or — opt-in, with a warning — the browser's own recognizer.
const synth = window.speechSynthesis || null;
S.speak = { id: null };
export const SPEAK_BTN = () => synth ? `<button class="icon-btn" data-act="speak" title="Read aloud">${ic('volume')}</button>` : '';
const localVoices = () => (synth?.getVoices() || []).filter(v => v.localService);

function speechText(content) {
  const st = splitThink(content), d = document.createElement('div');
  d.innerHTML = renderMD(st ? st.content : content);
  d.querySelectorAll('pre, table').forEach(x => x.replaceWith(document.createTextNode(' (code omitted). ')));
  d.querySelectorAll('sup, .copy-code').forEach(x => x.remove());
  d.querySelectorAll('p, li, h1, h2, h3, h4, h5, h6, blockquote, br').forEach(x => x.append('\n'));
  return d.textContent.replace(/\[\d+\]/g, '').replace(/https?:\/\/\S+/g, ' link ').replace(/[ \t]+/g, ' ').replace(/[*_`#>]/g, '').trim();
}
function speechChunks(t) {
  const out = []; let cur = '';
  for (let p of t.split(/(?<=[.!?…。！？])\s+|\n+/).map(x => x.trim()).filter(Boolean)) {
    while (p.length > 220) { let i = p.lastIndexOf(' ', 220); if (i < 60) i = 220; out.push(p.slice(0, i)); p = p.slice(i).trim(); }
    if (cur && (cur + ' ' + p).length > 220) { out.push(cur); cur = p; } else cur = cur ? cur + ' ' + p : p;
  }
  if (cur) out.push(cur);
  return out;
}
function markSpeaking() {
  $$('#messages [data-act=speak]').forEach(b => {
    const on = b.closest('[data-id]')?.dataset.id === S.speak.id;
    b.classList.toggle('on', on); b.innerHTML = ic(on ? 'stop' : 'volume'); b.title = on ? 'Stop reading' : 'Read aloud';
  });
}
export function stopSpeak() { if (!synth) return; S.speak.id = null; synth.cancel(); markSpeaking(); }
function pickVoice() {
  const vs = localVoices();
  return vs.find(v => v.voiceURI === S.settings.ttsVoice) || vs.find(v => v.lang === navigator.language) || vs.find(v => v.lang.startsWith(navigator.language.slice(0, 2))) || vs[0];
}
function speakText(text, onEnd) {
  const v = pickVoice();
  if (!v) { toast('No on-device voices are installed, so read-aloud is unavailable', true); onEnd?.(); return false; }
  const parts = speechChunks(text);
  parts.forEach((t, i) => {
    const u = new SpeechSynthesisUtterance(t);
    u.voice = v; u.lang = v.lang; u.rate = Number(S.settings.ttsRate) || 1;
    if (i === parts.length - 1) { u.onend = u.onerror = () => onEnd?.(); }
    synth.speak(u);
  });
  return true;
}
export function speak(id, content) {
  if (!synth) return;
  synth.cancel();
  const text = speechText(content || '');
  if (!text) return;
  S.speak.id = id; markSpeaking();
  if (!speakText(text, () => { if (S.speak.id === id) { S.speak.id = null; markSpeaking(); } })) { S.speak.id = null; markSpeaking(); }
}

// ── dictation ──
S.mic = { rec: null, state: 'idle', timer: 0 };
function setMic(state) {
  S.mic.state = state;
  const b = $('#micBtn');
  b.classList.toggle('rec', state === 'rec'); b.classList.toggle('busy', state === 'busy');
  b.title = state === 'rec' ? 'Stop dictation' : state === 'busy' ? 'Transcribing…' : 'Dictate';
}
function insertAtCursor(text) {
  text = (text || '').trim(); if (!text) return;
  const t = $('#input'), a = t.selectionStart ?? t.value.length, b = t.selectionEnd ?? a;
  const before = t.value.slice(0, a), pre = before && !/\s$/.test(before) ? ' ' : '';
  t.value = before + pre + text + t.value.slice(b);
  t.setSelectionRange(a + pre.length + text.length, a + pre.length + text.length);
  autosize(); updateSendBtn(); t.focus();
}
function encodeWav(samples, rate) {
  const buf = new ArrayBuffer(44 + samples.length * 2), v = new DataView(buf);
  const w = (o, s) => [...s].forEach((c, i) => v.setUint8(o + i, c.charCodeAt(0)));
  w(0, 'RIFF'); v.setUint32(4, 36 + samples.length * 2, true); w(8, 'WAVEfmt '); v.setUint32(16, 16, true);
  v.setUint16(20, 1, true); v.setUint16(22, 1, true); v.setUint32(24, rate, true); v.setUint32(28, rate * 2, true); v.setUint16(32, 2, true); v.setUint16(34, 16, true);
  w(36, 'data'); v.setUint32(40, samples.length * 2, true);
  for (let i = 0; i < samples.length; i++) { const x = Math.max(-1, Math.min(1, samples[i])); v.setInt16(44 + i * 2, x < 0 ? x * 0x8000 : x * 0x7fff, true); }
  return new Blob([buf], { type: 'audio/wav' });
}
async function blobToWav16k(blob) {
  const ac = new (window.AudioContext || window.webkitAudioContext)();
  let decoded; try { decoded = await ac.decodeAudioData(await blob.arrayBuffer()); } finally { ac.close(); }
  const off = new OfflineAudioContext(1, Math.max(1, Math.ceil(decoded.duration * 16000)), 16000);
  const src = off.createBufferSource(); src.buffer = decoded; src.connect(off.destination); src.start();
  return encodeWav((await off.startRendering()).getChannelData(0), 16000);
}
async function transcribeWav(wav) {
  const q = new URLSearchParams({ url: S.settings.whisperUrl, style: S.settings.whisperStyle, model: S.settings.whisperModel || '', lang: S.settings.sttLang || '' });
  const r = await fetch('/api/transcribe?' + q, { method: 'POST', headers: { 'X-Guanaco': '1', 'Content-Type': 'audio/wav' }, body: wav });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || r.statusText);
  return j.text || '';
}
async function startWhisper() {
  let stream;
  try { stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } }); }
  catch (e) { toast('Microphone unavailable: ' + (e.message || e.name), true); return; }
  const chunks = [], mr = new MediaRecorder(stream);
  mr.ondataavailable = e => { if (e.data.size) chunks.push(e.data); };
  mr.onstop = async () => {
    stream.getTracks().forEach(t => t.stop()); clearTimeout(S.mic.timer); setMic('busy');
    try { insertAtCursor(await transcribeWav(await blobToWav16k(new Blob(chunks, { type: mr.mimeType })))); }
    catch (e) { toast('Dictation failed: ' + e.message, true); }
    finally { setMic('idle'); }
  };
  mr.start(); S.mic.rec = mr; setMic('rec');
  S.mic.timer = setTimeout(stopMic, 120000);
}
function startBrowserDictation() {
  const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
  if (!SR) { toast('This browser has no built-in dictation — use a local whisper server instead', true); return; }
  const rec = new SR(), input = $('#input'), base = input.value;
  rec.continuous = true; rec.interimResults = true; rec.lang = S.settings.sttLang || navigator.language;
  if ('processLocally' in rec) rec.processLocally = true;
  rec.onresult = e => {
    let fin = '', interim = '';
    for (let i = 0; i < e.results.length; i++) (e.results[i].isFinal ? (fin += e.results[i][0].transcript) : (interim += e.results[i][0].transcript));
    const add = (fin + interim).trim();
    input.value = base + (base && add && !/\s$/.test(base) ? ' ' : '') + add; autosize(); updateSendBtn();
  };
  rec.onerror = e => { if (e.error !== 'aborted' && e.error !== 'no-speech') toast('Dictation: ' + e.error, true); };
  rec.onend = () => setMic('idle');
  try { rec.start(); S.mic.rec = rec; setMic('rec'); } catch (e) { toast('Dictation: ' + e.message, true); }
}
function stopMic() { try { S.mic.rec?.stop(); } catch { } }
function toggleMic() {
  if (S.mic.state === 'rec') return stopMic();
  if (S.mic.state === 'busy') return;
  if (S.settings.sttMode === 'whisper') startWhisper(); else if (S.settings.sttMode === 'browser') startBrowserDictation();
}
function applyVoiceUI() {
  $('#micBtn').classList.toggle('hidden', S.settings.sttMode === 'off');
}
function renderVoice() {
  const vs = localVoices();
  $('#ttsVoice').innerHTML = '<option value="">System default</option>' + vs.map(v => `<option value="${esc(v.voiceURI)}">${esc(v.name)} (${esc(v.lang)})</option>`).join('');
  $('#ttsVoice').value = S.settings.ttsVoice;
  $('#ttsNote').textContent = !synth ? 'This browser has no speech synthesis.' : vs.length ? `${vs.length} on-device voice${vs.length === 1 ? '' : 's'} available. Online/cloud voices are hidden on purpose.` : 'No on-device voices found — install a voice in your operating system.';
  $('#ttsAuto').checked = !!S.settings.ttsAuto; $('#ttsRate').value = S.settings.ttsRate; $('#ttsRateVal').textContent = Number(S.settings.ttsRate).toFixed(2) + '×';
  $('#sttMode').value = S.settings.sttMode;
  $('#whisperBox').classList.toggle('hidden', S.settings.sttMode !== 'whisper');
  $('#browserSttNote').classList.toggle('hidden', S.settings.sttMode !== 'browser');
  $('#whisperUrl').value = S.settings.whisperUrl; $('#whisperStyle').value = S.settings.whisperStyle; $('#whisperModel').value = S.settings.whisperModel; $('#sttLang').value = S.settings.sttLang;
  $('#whisperModelRow').classList.toggle('hidden', S.settings.whisperStyle !== 'openai');
}
export function bindVoice() {
  if (synth) synth.onvoiceschanged = () => { if (!$('#voiceModal').classList.contains('hidden')) renderVoice(); };
  $('#voiceBtn').onclick = () => { $('#prefsModal').classList.add('hidden'); renderVoice(); $('#voiceModal').classList.remove('hidden'); };
  $('#ttsAuto').onchange = e => setSetting('ttsAuto', e.target.checked);
  $('#ttsVoice').onchange = e => setSetting('ttsVoice', e.target.value);
  $('#ttsRate').oninput = e => { setSetting('ttsRate', Number(e.target.value)); $('#ttsRateVal').textContent = Number(e.target.value).toFixed(2) + '×'; };
  $('#ttsTest').onclick = () => { if (!synth) return; synth.cancel(); speakText('This is how replies will sound when read aloud.'); };
  $('#sttMode').onchange = async e => {
    const v = e.target.value;
    if (v === 'browser') {
      const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
      const local = SR && 'processLocally' in SR.prototype;
      const ok = await ask({ title: 'Use browser dictation?', msg: local ? 'Your browser can transcribe on-device, but it may fall back to its vendor\'s servers if the language pack isn\'t installed.' : 'Chrome and Edge send dictation audio to Google or Microsoft servers. For fully local dictation, use a whisper server instead.', input: false, okText: 'Use it anyway' });
      if (!ok) { e.target.value = S.settings.sttMode; return; }
    }
    setSetting('sttMode', v); applyVoiceUI(); renderVoice();
  };
  $('#whisperUrl').oninput = e => setSetting('whisperUrl', e.target.value.trim());
  $('#whisperStyle').onchange = e => { setSetting('whisperStyle', e.target.value); renderVoice(); };
  $('#whisperModel').oninput = e => setSetting('whisperModel', e.target.value.trim());
  $('#sttLang').oninput = e => setSetting('sttLang', e.target.value.trim());
  $('#whisperTest').onclick = async () => {
    const m = $('#whisperMsg'); m.textContent = 'Testing…'; m.classList.remove('err');
    try { await transcribeWav(encodeWav(new Float32Array(8000), 16000)); m.textContent = 'Connected — the server accepted audio.'; }
    catch (err) { m.textContent = err.message; m.classList.add('err'); }
  };
  $('#micBtn').onclick = toggleMic;
  applyVoiceUI();
}
