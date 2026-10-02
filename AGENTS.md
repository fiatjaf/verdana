# Repository Guidelines

## Project Structure & Module Organization

Verdana has two Go modules and one Android application:

- `backend/` contains shared application logic, Nostr integration, storage, permissions, and the embedded web UI under `backend/webview/`. Go tests and fixtures live beside the code in `*_test.go` and `backend/testdata/`.
- `desktop/` contains the Gio desktop launcher and its child webview host. Fonts and other packaged resources are in `desktop/assets/`.
- `android/` is a Gradle/Kotlin app. Native sources are under `android/app/src/main/java/com/verdana/app/`, with resources in `res/`.
- `.github/workflows/` documents the supported CI builds for desktop and Android.

## Build, Test, and Development Commands

Run commands from the repository root unless noted:

- `just run` builds the child host and launches a development desktop build with webview debugging enabled.
- `just prod` builds the production desktop binary at `desktop/verdana`.
- `cd backend && go test ./...` runs backend tests.
- `cd desktop && go build -o child/child ./child && go test -tags novulkan ./...` reproduces desktop CI; the child binary must exist before package compilation.
- `just apk` builds the backend AAR and Android debug APK. It requires the Android SDK at `/opt/android-sdk` and installed `gomobile` tooling.
- `just fonts` regenerates embedded WOFF2 files after changing `desktop/assets/*.ttf`.

## Coding Style & Naming Conventions

Format Go changes with `gofmt`; use tabs as emitted by the formatter, short lowercase package names, and `MixedCaps` identifiers. Keep platform-specific implementations in suffix files such as `shortcutfile_linux.go`. For Kotlin, use four-space indentation, `PascalCase` types, and `camelCase` members. Preserve the existing plain JavaScript and CSS style in `backend/webview/`; avoid adding a new toolchain for small UI changes.

## Testing Guidelines

Use Go's standard `testing` package. Name tests `TestBehavior` and place them alongside the implementation. Add fixtures to `backend/testdata/`. There is no stated coverage threshold, but changes to parsing, permissions, storage, networking, or napplet lifecycle behavior should include focused regression tests. Run both backend and desktop test commands before opening a PR.

## Commit & Pull Request Guidelines

After making significant changes, commit them before handing work back to the user. Recent commits use concise, imperative, mostly lowercase subjects (for example, `implement persistent localStorage.`). Keep each commit focused and explain non-obvious rationale in the body. PRs should summarize user-visible effects, list verification commands, link relevant issues, and include screenshots or recordings for desktop, Android, or webview UI changes. Do not commit generated binaries, APKs, AARs, or `desktop/dist/` artifacts.
