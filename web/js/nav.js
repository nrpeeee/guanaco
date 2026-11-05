// Navigation and routing between chats
import { fullChat } from './chats.js';
import { curProv, currentModel, findModel, renderModelList, updateModelUI } from './models.js';
import { renderAll, scrollToBottom } from './render.js';
import { S, saveSettings, setSetting } from './state.js';
import { presetById } from './system-prompt.js';
import { $, toast } from './util.js';
import { stopSpeak } from './voice.js';

// ───────────────────────── navigation ─────────────────────────
export function newChat() {
  stopSpeak();
  S.chat = null;
  S.draft = { system: presetById(S.draft.presetId)?.prompt ?? '', presetId: S.draft.presetId || 'default' };
  if (location.hash) history.replaceState(null, '', location.pathname);
  renderAll();
  $('#input').focus();
}

export async function openChat(id) {
  stopSpeak();
  try {
    const c = await fullChat(id);
    S.chat = c;
    // Restore the chat's own provider/model only if it is still available —
    // never swap a missing model for something else behind the user's back.
    if (c.model && findModel(c.provider, c.model) && (c.model !== currentModel() || c.provider !== curProv())) {
      S.settings.provider = c.provider || 'ollama'; S.settings.model = c.model; saveSettings();
      renderModelList(); updateModelUI();
    }
    history.replaceState(null, '', '#/c/' + id);
    renderAll();
    scrollToBottom(true);
    if (innerWidth <= 820) setSidebar(false);
  } catch (e) { toast('Could not open chat: ' + e.message, true); newChat(); }
}

export function setSidebar(open) {
  $('#app').classList.toggle('collapsed', !open);
  $('#openSidebar').classList.toggle('hidden', open);
  if (innerWidth > 820) setSetting('sidebarCollapsed', !open);
}
