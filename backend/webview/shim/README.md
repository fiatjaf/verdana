# Vendored @napplet/shim prelude

`prelude.global.js` is `dist/prelude.global.js` from
[`@napplet/shim`](https://github.com/napplet/web/tree/main/packages/shim)
**0.29.2** (MIT), with its intent domain patched to the current NAP-INTENT
request API and lifecycle-independent delivery API. The launcher inlines it into every
napplet's srcdoc and activates it with `NappletShimPrelude.install({domains})`,
so `window.napplet.*` exists before the napplet's own scripts run.

sha256: `cd65c1212427d7d488a34b99e776913267780d793acb4aec1e00506da1463473`

The Go NAP handlers (`backend/nap*.go`) are written against this build's wire
shapes. To upgrade, replace the file, update the version and hash here, and
re-check the handlers against the shim's `nap/src/*/shim.ts`.
