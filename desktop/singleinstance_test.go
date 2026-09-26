package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeTestPort(t *testing.T, dir string, port int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "launcher.port"), []byte(strconv.Itoa(port)), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestForwardToInstance(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	writeTestPort(t, dir, ln.Addr().(*net.TCPAddr).Port)

	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		var msg forwarded
		if err := json.NewDecoder(conn).Decode(&msg); err != nil {
			return
		}
		conn.Write([]byte("ok\n"))
		got <- msg.Token
	}()

	if !forwardToInstance(dir, "npub1abc +view:1 npub1def +open") {
		t.Fatal("forward reported failure")
	}
	if tok := <-got; tok != "npub1abc +view:1 npub1def +open" {
		t.Fatalf("token mangled: %q", tok)
	}
}

func TestForwardNoInstance(t *testing.T) {
	if forwardToInstance(t.TempDir(), "npub1abc") {
		t.Fatal("forwarded with no listener")
	}
}

func TestForwardStalePort(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	writeTestPort(t, dir, port)
	if forwardToInstance(dir, "npub1abc") {
		t.Fatal("forwarded to a dead launcher")
	}
}
