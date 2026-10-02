# Vendored @napplet/shim prelude

`prelude.global.js` is `dist/prelude.global.js` from
[`@napplet/shim`](https://github.com/napplet/web/tree/main/packages/shim)
**0.30.0** (MIT), with these Verdana compatibility patches:

- NAP-INTENT follows the current request API and lifecycle-independent delivery
  API.
- NAP-RESOURCE accepts legacy string entries in `bytesMany` while emitting the
  current per-request Blossom server-hint wire shape.
- NAP-COMMON writes do not time out while waiting for user approval.
- NAP-INC rejects empty query strings and query parameters without names.

The launcher inlines it into every napplet's srcdoc and activates it with
`NappletShimPrelude.install({domains})`, so `window.napplet.*` exists before
the napplet's own scripts run.

sha256: `777393736d2d3035d82b339de55863c0db8d6afeb3b93ad6ff999ec85a5b498e`

The Go NAP handlers (`backend/nap*.go`) are written against this build's wire
shapes. To upgrade, replace the file, update the version and hash here, and
re-check the handlers against the shim's `nap/src/*/shim.ts`.
