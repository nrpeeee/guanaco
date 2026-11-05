/* Guanaco — frontend entry point. Native ES modules, no build step, no frameworks.
   Shutdown signal and boot sequence. */
import { loadChats } from './chats.js';
import { autosize } from './composer.js';
import { bindRag, loadRag } from './documents.js';
import { bindEvents, idFromHash } from './events.js';
import { checkHealth } from './health.js';
import { loadCatalog } from './models.js';
import { openChat } from './nav.js';
import { loadProviders } from './providers.js';
import { greeting, renderAll } from './render.js';
import { bindSettings } from './settings-ui.js';
import { DEFAULTS, DEFAULT_PARAMS, DEFAULT_PRESETS, DEFAULT_PROMPTS, S } from './state.js';
import { bindTools, loadMcp } from './tools.js';
import { $, api, hydrateIcons } from './util.js';
import { bindVoice } from './voice.js';

// ───────────────────────── shutdown ─────────────────────
// When the browser tab/window closes, signal the Go process to exit so it
// doesn't linger in the background. sendBeacon fires during unload and can't
// set headers, so /shutdown takes the per-run token in the body instead. A
// reload sends this too; the server waits a beat and cancels if the page
// comes back, so F5 doesn't leave you with a dead tab. Each tab also checks in
// on a timer, and the server only exits once no other tab is still alive.
// In -lan mode the server hands out no token and none of this runs.
const TAB_ID = 't' + crypto.randomUUID();
const heartbeat = () => api('/tabs/alive', { method: 'POST', body: JSON.stringify({ id: TAB_ID }) }).catch(() => { });
window.addEventListener('beforeunload', () => {
  if (S.shutdownToken) navigator.sendBeacon('/shutdown', JSON.stringify({ token: S.shutdownToken, tab: TAB_ID }));
});
document.addEventListener('visibilitychange', () => { if (S.shutdownToken && document.visibilityState === 'visible') heartbeat(); });

// ───────────────────────── boot ─────────────────────────
(async function boot() {
  hydrateIcons();
  try {
    const saved = await api('/settings');
    S.settings = { ...structuredClone(DEFAULTS), ...saved, params: { ...DEFAULT_PARAMS, ...(saved.params || {}) } };
    if (!Array.isArray(S.settings.presets) || !S.settings.presets.length) S.settings.presets = structuredClone(DEFAULT_PRESETS);
    if (!Array.isArray(S.settings.prompts)) S.settings.prompts = structuredClone(DEFAULT_PROMPTS);
    if (!S.settings.provider) S.settings.provider = 'ollama';
  } catch { }
  try { S.shutdownToken = (await api('/shutdown-token')).token; } catch { }
  if (S.shutdownToken) { heartbeat(); setInterval(heartbeat, 20000); }
  S.draft = { system: '', presetId: 'default' };
  bindSettings();
  bindEvents(); bindRag(); bindVoice(); bindTools();
  if (S.settings.sidebarCollapsed || innerWidth <= 820) { $('#app').classList.add('collapsed'); $('#openSidebar').classList.remove('hidden'); }
  await Promise.all([checkHealth(), loadProviders()]);
  await Promise.all([loadCatalog(), loadChats(), loadRag(), loadMcp()]);
  const id = idFromHash();
  if (id && S.chats.find(c => c.id === id)) await openChat(id); else renderAll();
  autosize();
  $('#input').focus();
  setInterval(checkHealth, 10000);
  setInterval(() => { if ($('#app').classList.contains('empty')) $('#greeting').innerHTML = greeting(); }, 60000);
})();
