# Napplets in Verdana

Verdana runs two kinds of apps:

- **napps** (kind `35130`): a file tree in a webview, with `window.nostr`,
  `window.nostrdb` and `window.napp` injected (see `env.d.ts`).
- **napplets** (kinds `35129` and `15129`, [napplet.run](https://napplet.run)):
  one self-contained HTML file in a sandboxed iframe, reaching the launcher
  only through NAP messages (`window.napplet.*`). The runtime follows NIP-5D.

Both appear in the same lists; napplets carry a "napplet" badge.

## Manifest schemas

Two napplet manifest shapes are in use, and both are read. The presence of
`path` tags tells them apart (`backend/napplet.go`, `napplet_nip5d.go`):

| | NIP-5D ([nips#2303](https://github.com/nostr-protocol/nips/pull/2303)) | naps `WEB-NAPPLET.md` |
|---|---|---|
| Kinds | `35129` (named, `d`), `15129` (root, one per author) | `35129` |
| Files | NIP-5A `path` tags; only `/index.html` runs | one blob, hash in `x` |
| `x` | NIP-5A aggregate of the `path` tags. It is recomputed and must match when present | sha256 of the HTML |
| Text | `title`, `description` (content is often empty) | `title`, content (required) |
| Routing | `archetype <role> <convention>` | `z`, `i` |
| Domains | `requires`: unsupported ones are flagged in the detail view | `R`/`O`: display only, never a warning |

Validation of the NIP-5D shape is lenient for display tags and strict for the
content address. Path tags that escape the napplet's directory are refused.
WEB-NAPPLET events get every MUST in that spec, including refusing `requires`
or `C` tags without `path` tags. Other kind 35129 events with neither shape
are skipped. Kind `5129` snapshots are not read yet.

## How a napplet runs

```
napplet window (desktop child process / Android NappActivity)
└─ main frame: launcher host page  (backend/webview/napplet-host.{html,js})
   └─ <iframe sandbox="allow-scripts" srcdoc=…>
        CSP meta · @napplet/shim prelude · install({domains}) + shell.ready · napplet HTML
```

1. **Install** downloads every listed blob, checks its sha256 and writes it
   under `napps/<id>/`.
2. **Launch** opens a window with `WindowSpec.Format = "napplet"`. The shell
   loads the host page. It does not load the napp's files or `bridge.js`.
3. The host page calls `nap.boot`. The backend re-hashes `index.html`
   against its manifest hash. It then builds a trusted wrapper
   (`buildSrcdoc`): the launcher's own `<!doctype html><html><head>`
   containing
   - the NIP-5D CSP (`connect-src 'none'`, no frames or workers, etc.);
   - the vendored shim (`backend/webview/shim/`);
   - an activation script that installs `window.napplet` for the domains the
     launcher implements, then posts `shell.ready`.

   The napplet's bytes follow verbatim. The napplet's HTML is never parsed
   to find an insertion point, so nothing it contains (a comment, or a script
   mentioning `<head>`) can get ahead of the CSP.
4. Every envelope from the frame is checked by the host page
   (`event.source === iframe.contentWindow`) and forwarded as `nap.msg`.
   Envelopes go one at a time to keep their order. The backend
   (`backend/nap.go`) handles them sequentially per window. Replies and
   pushes return through `window.__nap_push`, which re-posts them into the
   frame.

A napplet window speaks only `nap.*`: `bridgeRPC` refuses every `window.napp`
rpc for it.

## Domains

| Domain | Where | Notes |
|---|---|---|
| `shell` | `nap.go` | `shell.ready` → `shell.init {capabilities:{domains}}`; a second `shell.ready` (a reload) starts a new session |
| `relay` | `nap_relay.go` | subscribe/close/query use the outbox model. publish/publishEncrypted show **one** prompt, then encrypt, sign and publish. The user's NIP-04 DMs are decrypted before delivery (asked once per session) |
| `identity` | `nap_identity.go` | read-only; `identity.changed` on login/logout |
| `storage` | `nap_basic.go` | 512 KB, shared or per-window scope. Keyed by the napplet's **address**, not its artifact hash, so data survives updates (a deliberate deviation from NAP-STORAGE) |
| `theme` | `nap_basic.go` | launcher `surface/text/accent` → `background/text/primary`; `theme.changed` on switch |
| `link` | `nap_basic.go` | http(s) only, behind the open-link prompt |
| `common` | `nap_common.go` | follow/unfollow (kind 3), react (7), report (1984), getProfile, follows, encode/decodeNip19 (never `nsec`) |
| `inc` | `nap_inc.go` | topics (exact match, never echoed back to the sender) and channels; the sender is always stamped by the launcher |
| `intent` | `nap_intent.go` | `napplet:<archetype>/<action>` is routed through the launcher's action system (picker, rules, cold launch). Napplets receive it as an `inc.event` once they listen on the topic. Napps can handle intents by declaring the same action string |
| `resource` | `nap_resource.go` | `data:`, `https:`, `blossom:sha256:`, `nostr:`. Public addresses only (checked at dial time on every hop), 10 MiB, 30 s, MIME sniffed, no SVG or HTML. Web fetches are asked once per session |

Not implemented yet: `notify`, `keys`, `config`, `media`, `outbox`, `upload`,
`lists`, `dm`, `count`, and decrypting NIP-17/59 gift wraps.

## Security notes

- The iframe never gets `allow-same-origin`, so it has an opaque origin, no
  storage and no access to the host page.
- **Desktop:** in WebKitGTK the sandboxed frame *can* reach
  `window.webkit.messageHandlers` and post forged binding calls (verified).
  So in napplet windows both bindings (`__verdana_napplet_rpc` and
  `__verdana_napplet_answer`) require a per-window random token. Only the
  top-frame init script knows it (`desktop/child/napplet.go`).
- **Android:** the injected scripts and the `__verdanaHost` listener are
  scoped to the window's https origin, which the opaque-origin frame never
  matches.
- `R`/`O` tags are display-only. Which domains a napplet gets is the
  launcher's policy (`napDomains`).

## Developing a napplet

Build a **single-file** napplet (e.g. `@napplet/vite-plugin` with
`artifactMode: "single-file"`). Put the output in a folder with a
`metadata.json`:

```json
{
  "id": "my-napplet",
  "format": "napplet",
  "title": "My napplet",
  "description": "What it does",
  "icon": "/icon.png",
  "roles": ["profile"],
  "conventions": [{ "id": "napplet:profile/open", "params": ["pubkey"] }]
}
```

Load the folder from the Dev tab. **Reload** swaps the bytes into open
windows in place. **Publish** uploads `index.html` (and the icon) to Blossom
and signs a kind `35129` event. Dev-server URLs are not supported for
napplets, because a module graph cannot run under the napplet CSP.

## Updating the shim

`backend/webview/shim/prelude.global.js` is `@napplet/shim` 0.29.2, copied
unmodified (see `shim/README.md`). To upgrade:

1. Replace the file.
2. Update the version/hash in `shim/README.md` and `ShimVersion`.
3. Re-check the handlers against `nap/src/*/shim.ts`.
4. Run `go test ./backend/...`.
