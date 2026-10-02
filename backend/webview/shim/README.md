# Vendored @napplet/shim prelude

`prelude.global.js` is `dist/prelude.global.js` from
[`@napplet/shim`](https://github.com/napplet/web/tree/main/packages/shim)
**0.29.2** (MIT), with its intent domain patched to the lifecycle-independent
API in napplet/naps PR #91 and its resource domain patched for the current
per-request Blossom server-hint wire shape. Its common domain is patched so
`follow`, `unfollow`, `react` and `report` have no 30s timeout: they wait on
the user's approval prompt, as `relay.publish` does. The launcher inlines it into every
napplet's srcdoc and activates it with `NappletShimPrelude.install({domains})`,
so `window.napplet.*` exists before the napplet's own scripts run.

sha256: `0980d80f2b5ae36afc9f2528ef98f7f9fd1929398af77799dccb6f14c176a8a7`

The Go NAP handlers (`backend/nap*.go`) are written against this build's wire
shapes. To upgrade, replace the file, update the version and hash here and in
`embed.go`, and re-check the handlers against the shim's `nap/src/*/shim.ts`.
