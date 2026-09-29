run:
    cd desktop && go build -o child/child ./child && go build -o verdana -tags 'dev,novulkan' && WEBVIEW_DEBUG=true ./verdana

prod:
    cd desktop && go build -o child/child ./child && go build -o verdana -tags 'novulkan' .

# the ui kit carries the launcher's own face (desktop/assets/*.ttf is
# Verdana, in three faces) as woff2, inlined by backend/webview/embed.go.
# Regenerate them when those ttf files change.
fonts:
	(cd desktop/assets && woff2_compress v.TTF && woff2_compress vb.ttf && woff2_compress vi.ttf)
	cp desktop/assets/v.woff2 desktop/assets/vb.woff2 desktop/assets/vi.woff2 backend/webview/fonts/
	rm -f desktop/assets/*.woff2

# android targets: the aar is rebuilt only when the backend changed (a gomobile
# bind of everything takes a while), the apk takes it from app/libs.
aar:
    mkdir -p android/app/libs
    cd backend && ANDROID_HOME=/opt/android-sdk gomobile bind -target=android -androidapi 26 -o ../android/app/libs/backend.aar ./mobile

apk: aar
    cd android && ./gradlew assembleDebug

install: aar
    cd android && ./gradlew installDebug
