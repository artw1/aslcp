# ASLCP web UI

Svelte 5 (runes) + Vite + TypeScript + Tailwind CSS v4 single-page app. Talks to the Go server through the HTTP/SSE API in `../docs/API.md`.

## Develop

```sh
npm ci
npm run dev        # Vite on :5173, proxies /api to http://localhost:8080 (the Go server; override with ASLCP_BACKEND)
npm run dev:mock   # no backend needed: in-memory API + simulated keying (mock/plugin.ts)
npm run check      # svelte-check / TypeScript
```

Mock logins: `admin`, `op` (operator) or `guest` (viewer), password `pass`. `MOCK_SETUP=1 npm run dev:mock` starts in first-run setup mode (setup code `123456`).

## Build and embed

```sh
npm run build      # outputs web/dist
```

The repository Makefile copies `web/dist` into `internal/webui/dist`, which the Go binary embeds with `go:embed` and serves at `/` (unknown non-`/api` paths fall back to `index.html`). Routing is hash-based (`#/`, `#/lookup`, `#/settings`), so the server needs no route knowledge.

## Layout

- `src/lib/` API client (`api.ts`), SSE + state stores (`store.svelte.ts`), actions, theme, router, toasts.
- `src/components/` pages and widgets (Dashboard, ConnectBar, LinksTable, Favorites, Lookup, Settings, ...).
- `mock/plugin.ts` dev-only Vite plugin, enabled with `VITE_MOCK=1`; never included in the build output.
