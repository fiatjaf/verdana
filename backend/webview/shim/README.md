# Vendored @napplet/shim prelude

`prelude.global.js` is `dist/prelude.global.js` from
[`@napplet/shim`](https://github.com/napplet/web/tree/main/packages/shim)
**0.30.0** (MIT), with these Verdana compatibility patches:

- NAP-INTENT follows the current request API and lifecycle-independent delivery
  API.
- NAP-RESOURCE accepts legacy string entries in `bytesMany` while emitting the
  current per-request Blossom server-hint wire shape.
- Requests carry no shim-side deadline (`napRequestTimer`): the shell answers
  every request, even one waiting on the user's approval or a remote signer
  (a reload discards the document along with its pending requests). A
  `timeoutMs` the napplet passes itself is still honored.
- NAP-INC rejects empty query strings and query parameters without names.
- NAP-NOTIFY remembers the shell's latest control list so an `onControls`
  subscriber registered after `shell.init` still receives initial capabilities.
- NAP-SHELL exposes the mandatory `window.napplet.shell` API and consumes the
  launcher's `shell.init` capability environment.

The launcher inlines it into every napplet's srcdoc and activates it with
`NappletShimPrelude.install({domains})`, so `window.napplet.*` exists before
the napplet's own scripts run.

sha256: `6d98d7ba5fb6b0c66550f1e43bf3a4e97880f9907a697bcbe6d58bf89759ed18`

The Go NAP handlers (`backend/nap*.go`) are written against this build's wire
shapes. To upgrade, replace the file, update the version and hash here, and
re-check the handlers against the shim's `nap/src/*/shim.ts`.
