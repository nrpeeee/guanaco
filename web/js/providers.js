// Provider lookup helpers
import { S } from './state.js';
import { api } from './util.js';

// ───────────────────────── providers ─────────────────────────
export const provById = id => S.providers.find(p => p.id === (id || 'ollama'));
export const provName = id => provById(id)?.name || (id === 'ollama' || !id ? 'Ollama' : id);
export const isLocal = id => { const p = provById(id); return p ? p.local : (!id || id === 'ollama'); };
export const provParams = id => provById(id)?.params || ['temperature', 'top_p', 'top_k', 'repeat_penalty', 'num_ctx', 'max_tokens', 'seed', 'keep_alive'];

export async function loadProviders() {
  try {
    const r = await api('/providers');
    S.providers = r.providers || []; S.protection = r.protection || ''; S.lan = !!r.lan;
  } catch { }
}
