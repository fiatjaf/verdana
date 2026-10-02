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
- NAP-NOTIFY remembers the shell's latest control list so an `onControls`
  subscriber registered after `shell.init` still receives initial capabilities.

The launcher inlines it into every napplet's srcdoc and activates it with
`NappletShimPrelude.install({domains})`, so `window.napplet.*` exists before
the napplet's own scripts run.

sha256: `f35282bdeb0a9b30c1a6bb04c2e8ed05278ba89e4ccce1482f8e6f4b002dafed`

The Go NAP handlers (`backend/nap*.go`) are written against this build's wire
shapes. To upgrade, replace the file, update the version and hash here, and
re-check the handlers against the shim's `nap/src/*/shim.ts`.
