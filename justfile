run:
    go build -o child/child ./child && go build -tags dev && WEBVIEW_DEBUG=true ./verdana

prod:
    go build -o child/child ./child && go build .
