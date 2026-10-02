package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

type previewTestHost struct {
	noopHost
	spec WindowSpec
}

func (h *previewTestHost) OpenWindow(spec WindowSpec) (Transport, error) {
	h.spec = spec
	return newRecTransport(), nil
}

func TestTryNappletLaunchesVerifiedDocumentWithoutInstalling(t *testing.T) {
	setupNapTest(t)
	stateMu.Lock()
	state.BlossomServers = []string{}
	stateMu.Unlock()
	document := []byte("<!doctype html><title>preview</title>")
	sum := sha256.Sum256(document)
	hash := hex.EncodeToString(sum[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+hash {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(document)
	}))
	defer server.Close()

	previousHost := host
	h := &previewTestHost{}
	host = h
	t.Cleanup(func() { host = previousHost })

	n := Napp{
		ID:      "napplet~0123456789abcdef~preview",
		D:       "preview",
		Name:    "Preview",
		Format:  FormatNapplet,
		Kind:    KindNapplet,
		Paths:   []NappPath{{Path: "/index.html", Sha256: hash}},
		Servers: []string{server.URL},
	}
	if err := tryNapplet(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	open := runningForNapp(n.ID)
	if len(open) != 1 {
		t.Fatalf("open preview instances: %d", len(open))
	}
	t.Cleanup(func() { WindowClosed(open[0].instance) })
	got, err := nappletDocumentForInstance(open[0])
	if err != nil || !bytes.Equal(got, document) {
		t.Fatalf("preview document: %q, %v", got, err)
	}
	if _, ok := InstalledNapp(n.ID); ok {
		t.Fatal("preview was recorded as installed")
	}
	if _, err := os.Stat(nappBaseDir(n.ID)); !os.IsNotExist(err) {
		t.Fatalf("preview wrote an install directory: %v", err)
	}
	if h.spec.Format != FormatNapplet || h.spec.NappID != n.ID {
		t.Fatalf("window spec: %+v", h.spec)
	}
}

func TestTryNappletRejectsNapps(t *testing.T) {
	setupNapTest(t)
	if err := tryNapplet(context.Background(), Napp{ID: "napp"}); err == nil {
		t.Fatal("ordinary napp was accepted for preview")
	}
}
