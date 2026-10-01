# Vendored @napplet/shim prelude

`prelude.global.js` is `dist/prelude.global.js` from
[`@napplet/shim`](https://github.com/napplet/web/tree/main/packages/shim)
**0.29.2** (MIT), with its intent domain patched to the lifecycle-independent
API in napplet/naps PR #91. The launcher inlines it into every
napplet's srcdoc and activates it with `NappletShimPrelude.install({domains})`,
so `window.napplet.*` exists before the napplet's own scripts run.

sha256: `d2058f9608c158f44bf25cd315980b375fa96dbb914bef4965ad0799368ba3a6`

The Go NAP handlers (`backend/nap*.go`) are written against this build's wire
shapes. To upgrade, replace the file, update the version and hash here and in
`embed.go`, and re-check the handlers against the shim's `nap/src/*/shim.ts`.
