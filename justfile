# A napp window is its own process: child/ is the webview shell, wasmchild/
# is the canvas shell a wasm napp paints into. Both are built before the
# launcher, because a release build embeds them.
run: build
    cd desktop && WEBVIEW_DEBUG=true ./verdana

build:
    cd desktop && go build -o child/child ./child && go build -o wasmchild/wasmchild ./wasmchild && go build -o verdana -tags dev

prod:
    cd desktop && go build -o child/child ./child && go build -o wasmchild/wasmchild ./wasmchild && go build -o verdana .

# installs the desktop app for the current user (binary, menu entry and icon
# under ~/.local, or /usr/local when run as root), the same way the release
# tarballs do.
install: prod
    #!/bin/sh
    set -eu
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    cp desktop/verdana packaging/linux/install.sh packaging/linux/com.verdana.app.desktop "$tmp/"
    cp desktop/assets/icon.png "$tmp/com.verdana.app.png"
    sh "$tmp/install.sh"

# the ui kit carries the launcher's own face (desktop/assets/*.ttf is
# Verdana, in three faces) as woff2, inlined by backend/webview/embed.go.
# Regenerate them when those ttf files change.
fonts:
	(cd desktop/assets && woff2_compress v.TTF && woff2_compress vb.ttf && woff2_compress vi.TTF)
	cp desktop/assets/v.woff2 desktop/assets/vb.woff2 desktop/assets/vi.woff2 backend/webview/fonts/
	rm -f desktop/assets/*.woff2

# android targets: the aar is rebuilt only when the backend changed (a gomobile
# bind of everything takes a while), the apk takes it from app/libs.
aar:
    mkdir -p android/app/libs
    cd backend && ANDROID_HOME=/opt/android-sdk gomobile bind -target=android -androidapi 26 -o ../android/app/libs/backend.aar ./mobile

apk: aar
    cd android && ./gradlew assembleDebug

install-android: aar
    cd android && ./gradlew installDebug

# linux packaging lives in packaging/linux: this builds the flatpak from the
# working tree and installs it for the current user (deb/rpm/arch come from
# nfpm in CI).
flatpak:
    flatpak-builder --user --install --force-clean build-flatpak packaging/linux/com.verdana.app.yml
