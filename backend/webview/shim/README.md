# Vendored @napplet/shim prelude

`prelude.global.js` is `dist/prelude.global.js` from
[`@napplet/shim`](https://github.com/napplet/web/tree/main/packages/shim)
**0.29.2** (MIT), copied unmodified. The launcher inlines it into every
napplet's srcdoc and activates it with `NappletShimPrelude.install({domains})`,
so `window.napplet.*` exists before the napplet's own scripts run.

sha256: `d3539080c553841d0387511cd8c902f15da8122075e0343380ff82a7beab898f`

The Go NAP handlers (`backend/nap*.go`) are written against this build's wire
shapes. To upgrade, replace the file, update the version and hash here and in
`embed.go`, and re-check the handlers against the shim's `nap/src/*/shim.ts`.
