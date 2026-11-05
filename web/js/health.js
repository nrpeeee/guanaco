// Backend health indicator
import { loadCatalog } from './models.js';
import { S } from './state.js';
import { $, api, toast } from './util.js';

// ───────────────────────── health ─────────────────────────
// Look for Ollama on its usual addresses; if it isn't running and is installed,
// offer to start it. quiet = automatic attempt (detect only, no toasts unless found).
let fixing = false;
export async function ollamaFix(quiet = false) {
  if (fixing) return; fixing = true;
  try {
    if (!quiet) toast('Looking for Ollama…');
    const d = await api('/ollama/detect', { method: 'POST' });
    if (d.ok) {
      if (!quiet || d.changed) toast(d.changed ? `Ollama found at ${d.url}` : 'Ollama is running');
      await checkHealth(); loadCatalog(); return;
    }
    if (quiet) return;
    if (d.installed || S.ollamaInstalled) {
      toast('Starting Ollama…');
      await api('/ollama/start', { method: 'POST' });
      toast('Ollama started'); await checkHealth(); loadCatalog(); return;
    }
    toast('Ollama not found. Install it from ollama.com, then click again', true);
  } catch (e) { if (!quiet) toast(e.message, true); }
  finally { fixing = false; }
}

export async function checkHealth() {
  const st = $('#ollamaStatus');
  try {
    const h = await api('/health');
    const was = S.online; S.online = h.ok;
    st.className = 'status ' + (h.ok ? 'ok' : 'bad');
    $('.txt', st).textContent = h.ok ? `Ollama ${h.ollama || ''}` : 'Ollama offline';
    st.title = h.ollama_url; $('#ollamaUrlTxt').textContent = h.ollama_url;
    S.ollamaInstalled = !!h.installed;
    st.classList.toggle('fixable', !h.ok);
    if (!h.ok) st.title = 'Ollama is not running. Click to detect or start it';
    if (h.ok && !was) loadCatalog();
    if (!h.ok && !S.autoDetected) { S.autoDetected = true; ollamaFix(true); }
  } catch {
    S.online = false; st.className = 'status bad'; $('.txt', st).textContent = 'Guanaco server offline';
  }
}
