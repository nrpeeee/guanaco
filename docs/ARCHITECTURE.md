# Architecture

Guanaco is a single Go binary that serves a static web UI and a small JSON API.
There are no third-party Go dependencies and no front-end build step.

## Backend (`cmd/guanaco`)

| File(s) | Responsibility |
|---|---|
| `main.go` | Flags, wiring, HTTP server, the `guard` middleware (Host check, `X-Guanaco` header, CSP) |
| `api.go` | Routes and handlers, including the streaming `/api/chat` endpoint (NDJSON events: `status`, `sources`, `docs`, `tool_calls`, `delta`, `done`, `error`) |
| `providers.go`, `provider_*.go` | `Provider` interface (`Models`, `Stream`), the provider registry, and the Ollama / OpenAI-compatible / Anthropic / Gemini implementations |
| `ollama*.go` | Ollama URL detection and (optionally) starting `ollama serve` |
| `credstore*.go` | Encrypted API-key store (DPAPI on Windows, AES-256-GCM elsewhere) |
| `store.go`, `backup.go` | Chat files, settings, backup/restore zip |
| `rag.go` | Chunking, embeddings, on-disk vector index, retrieval and the prompt context |
| `mcp.go` | MCP stdio client and the tool-approval API |
| `search.go` | Web search (DuckDuckGo / SearXNG) and page fetching with SSRF protection |
| `voice.go` | Proxy to a local speech-to-text server |
| `*_windows.go`, `*_other.go` | Platform-specific bits |

Persistence rule: state is written to disk first and only then committed in
memory, so a failed save never leaves the running app disagreeing with disk.

## Frontend (`web/`)

`index.html` loads the vendored libraries as classic scripts, then
`js/main.js` as a native ES module. State lives in one shared object `S`
(`js/state.js`); modules import what they need from each other.

| Module | Responsibility |
|---|---|
| `util`, `state`, `providers`, `markdown` | Foundations: DOM helpers, `api()`, shared state, provider lookup, safe Markdown |
| `models`, `chats`, `nav` | Model catalog and picker, chat list, routing |
| `render`, `branches`, `compare` | Message rendering, branch navigation, side-by-side compare |
| `composer`, `send` | The input box and the send/stream loop |
| `system-prompt`, `prompts` | Profiles, system prompt panel, prompt library and slash menu |
| `documents`, `tools`, `voice` | RAG UI, MCP tools UI, read-aloud and dictation |
| `ollama-models`, `provider-settings`, `settings-ui`, `health`, `export-import` | Dialogs and status |
| `events`, `main` | Global event wiring; boot sequence |

`web/embed.go` embeds the UI into the binary. A Go test checks that every
relative import between modules resolves to an embedded file.

## Icons

`assets/guanaco.ico` / `guanaco.png` are the source icons. Go does not read them
at build time, so they reach users in three separate ways:

- **Windows `.exe` icon:** compiled in from `cmd/guanaco/rsrc_windows_*.syso`.
  After changing `assets/guanaco.ico`, regenerate them from `cmd/guanaco`:
  `go install github.com/tc-hib/go-winres@latest`, then
  `go-winres simply --icon ../../assets/guanaco.ico --product-name Guanaco --file-description "Guanaco - local AI chat" --original-filename Guanaco.exe --product-version 2.0.0 --file-version 2.0.0.0 --arch amd64,arm64`
- **Browser tab icon:** an inline SVG in `web/index.html`; it is independent of `assets/`.
- **Desktop shortcuts:** `scripts/install.bat` / `install.sh` read the files in `assets/` when they run.
