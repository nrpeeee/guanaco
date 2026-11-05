// Shared state object S, defaults and settings persistence
import { api, debounce } from './util.js';

// ───────────────────────── state ─────────────────────────
export const DEFAULT_PRESETS = [
  { id: 'default', name: 'Default', prompt: '' },
  { id: 'code', name: 'Coding assistant', prompt: 'You are an expert software engineer fluent in C, Go, Python, Rust and Bash. Give correct, idiomatic, production-quality code. Prefer complete runnable examples, explain non-obvious decisions briefly, and point out edge cases, undefined behavior and memory-safety issues.' },
  { id: 'sec', name: 'Security researcher', prompt: 'You are a senior security researcher specializing in embedded/IoT firmware, reverse engineering, and network protocols. Be technically precise, reference CWE classes where relevant, and reason step by step about attack surface, root cause, and mitigations.' },
  { id: 'concise', name: 'Concise', prompt: 'Answer as concisely as possible. No preamble, no summaries, no filler. Use code or bullet points only when they are clearer than prose.' },
];
export const DEFAULT_PROMPTS = [
  { id: 'p-review', name: 'Code review', text: 'Review this code for bugs, security issues and readability. List problems by severity, then show a fixed version:\n\n{{paste code}}' },
  { id: 'p-explain', name: 'Explain simply', text: 'Explain {{topic}} step by step, as if to someone smart who is new to it. Finish with one concrete example.' },
  { id: 'p-summary', name: 'Summarize', text: 'Summarize the following in five bullet points, then give a one-sentence takeaway:\n\n{{text}}' },
  { id: 'p-rewrite', name: 'Rewrite clearer', text: 'Rewrite this so it is clear and concise, keeping my meaning and tone. Show only the rewrite:\n\n{{text}}' },
  { id: 'p-translate', name: 'Translate', text: 'Translate the following into {{language}}. Keep formatting and technical terms intact:\n\n{{text}}' },
  { id: 'p-commit', name: 'Commit message', text: 'Write a conventional-commit message (subject under 72 chars, then a short body) for this diff:\n\n{{diff}}' },
];
export const DEFAULT_PARAMS = { temperature: 0.7, top_p: 0.9, top_k: 40, repeat_penalty: 1.1, num_ctx: 8192, num_predict: '', seed: '' };
export const PARAM_LABELS = { temperature: 'temperature', top_p: 'top-p', top_k: 'top-k', repeat_penalty: 'repeat penalty', num_ctx: 'context window', max_tokens: 'max tokens', seed: 'seed', keep_alive: 'keep-alive' };
export const DEFAULTS = {
  provider: 'ollama', model: '', userName: '', theme: 'dark', font: 'sans', autoTitle: true, enterSends: true,
  params: { ...DEFAULT_PARAMS }, keepAlive: '', thinkMode: 'auto', showThinking: true,
  searchOn: false, searchRewrite: true, searchResults: 4, searchEngine: 'duckduckgo', searxUrl: '',
  toolsOn: false, toolsAllow: [],
  ttsAuto: false, ttsVoice: '', ttsRate: 1, sttMode: 'off', whisperUrl: 'http://127.0.0.1:8081', whisperStyle: 'inference', whisperModel: '', sttLang: '',
  ragOn: false, ragProvider: 'ollama', ragModel: 'nomic-embed-text', ragK: 5, ragCloud: false,
  presets: DEFAULT_PRESETS, prompts: DEFAULT_PROMPTS, sidebarCollapsed: false, cloudHelpers: false, lastLocal: null,
};

export const S = {
  settings: structuredClone(DEFAULTS),
  providers: [],       // from /api/providers
  protection: '',
  lan: false,
  models: [],          // unified catalog entries {provider,id,name,local,...}
  catalogErrors: {},
  caps: {},            // "provider|model" -> {capabilities, details, ctx}
  chats: [],
  cache: new Map(),
  chat: null,
  draft: { system: '', presetId: 'default' },
  streams: new Map(),
  pending: [],
  online: false,
  modelQuery: '',
  slash: { open: false, items: [], idx: 0 },
  rag: { docs: [], busy: null },
  mcp: { servers: [], waiters: new Map() },
  menuFor: 'main',       // which button the model menu is choosing for: 'main' | 'cmp'
  cmp: { on: false, provider: '', model: '' },   // side-by-side compare (session only)
  shutdownToken: '',
};

export const saveSettings = debounce(() => api('/settings', { method: 'PUT', body: JSON.stringify(S.settings) }).catch(() => { }), 400);
export function setSetting(k, v) { S.settings[k] = v; saveSettings(); }
