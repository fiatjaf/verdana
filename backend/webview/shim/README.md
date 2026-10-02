# Vendored @napplet/shim prelude

`prelude.global.js` is `dist/prelude.global.js` from
[`@napplet/shim`](https://github.com/napplet/web/tree/main/packages/shim)
**0.30.0** (MIT), with these Verdana compatibility patches:

- NAP-INTENT follows the lifecycle-independent API in napplet/naps PR #91.
- NAP-RESOURCE accepts legacy string entries in `bytesMany` while emitting the
  current per-request Blossom server-hint wire shape.
- NAP-COMMON writes do not time out while waiting for user approval.
- NAP-INC rejects empty query strings and query parameters without names.

The launcher inlines it into every napplet's srcdoc and activates it with
`NappletShimPrelude.install({domains})`, so `window.napplet.*` exists before
the napplet's own scripts run.

sha256: `0980d80f2b5ae36afc9f2528ef98f7f9fd1929398af77799dccb6f14c176a8a7`

The Go NAP handlers (`backend/nap*.go`) are written against this build's wire
shapes. To upgrade, replace the file, update the version and hash here and in
`embed.go`, and re-check the handlers against the shim's `nap/src/*/shim.ts`.
