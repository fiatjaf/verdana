run:
    cd desktop && go build -o child/child ./child && go build -o verdana -tags 'dev,novulkan' && WEBVIEW_DEBUG=true ./verdana

prod:
    cd desktop && go build -o child/child ./child && go build -o verdana -tags 'novulkan' .

# android targets: the aar is rebuilt only when the backend changed (a gomobile
# bind of everything takes a while), the apk takes it from app/libs.
aar:
    cd backend && ANDROID_HOME=/opt/android-sdk gomobile bind -target=android -androidapi 26 -o ../android/app/libs/backend.aar ./mobile

apk: aar
    cd android && ./gradlew assembleDebug

install: aar
    cd android && ./gradlew installDebug
