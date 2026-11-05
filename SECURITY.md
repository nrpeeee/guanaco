# Security

## Reporting a vulnerability

Please report problems privately through GitHub's **Security → Report a
vulnerability** form on this repository (private security advisory) rather than
a public issue. Include the version (`Guanaco x.y.z` is printed at startup),
your OS, and steps to reproduce. I'll acknowledge reports as soon as I can.

## What Guanaco protects, and how

Guanaco is a **local, single-user** application. It is designed to be reached
only from your own computer.

- **Loopback only by default.** The server listens on `127.0.0.1`. Requests
  whose `Host` isn't `localhost`/`127.0.0.1`/`::1` are refused (DNS-rebinding
  defence), and every `/api/` call must carry an `X-Guanaco: 1` header, which
  forces a CORS preflight that is never answered, so other websites can't call it.
- **Strict CSP.** The page can only load its own scripts and connect to its own
  origin. Model output is rendered as Markdown and sanitised with DOMPurify.
- **API keys** live in a separate encrypted file, never in chats, settings or
  backups: Windows DPAPI on Windows (bound to your account), AES-256-GCM
  elsewhere with the key kept in your OS config directory (mode 0600).
  The API never returns a stored key, only whether one is set and its last 4
  characters.
- **Cloud providers are blocked in `-lan` mode** unless you also pass
  `-lan-cloud`, so other people on your network can't spend your API credits.
- **Documents (RAG)** are embedded with a *local* model only. Retrieved text is
  passed to the chat model as untrusted reference data. Cloud chat models are
  skipped unless you opt in.
- **Web search** results are treated as untrusted data, and page fetching refuses
  loopback and private addresses.
- **Tools (MCP)**: the model can *request* a tool call but never runs one. Each
  call is shown with its exact arguments and needs your approval. "Always allow"
  is per tool and is cancelled automatically if the server's command line changes.
  A server's own "read-only" label is shown but never trusted. Tools only work
  with local models and are disabled in `-lan` mode.
- **Dictation** audio is only ever sent to a speech-to-text server on your
  computer or local network.

## Things to be aware of

- **MCP servers are programs that run on your computer with your permissions.**
  Only add ones you trust; Guanaco can't sandbox them.
- **`-lan` mode has no authentication.** Use it only on a network you trust.
  Anyone who can reach it can read and change your chats and documents.
  Provider settings, keys and tool servers are read-only there, tool-server
  arguments and environment variables are hidden, and closing a tab never
  stops the server.
- Chats and documents are stored as **plain files** in the data folder
  (a per-user folder by default; see the README). Only API keys are encrypted.
  Use full-disk encryption if that matters for your threat model.
- Guanaco does not phone home or check for updates. Web search, cloud
  providers and tools only touch the network when you enable and use them.
