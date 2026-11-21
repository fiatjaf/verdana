package main

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"verdana/backend"
)

// maxForwardBytes bounds a single forwarded JSON payload on the local
// single-instance port so a local peer cannot exhaust memory.
const maxForwardBytes = 1 << 20

// Single-instance forwarding: the first launcher binds a localhost port and
// writes the number to launcher.port in its data dir. When a shortcut (or the
// user) starts verdana again with a bundle token, the new process finds the
// port, hands the token over and exits — the running launcher opens the
// bundle's napps. A nostr: link the launcher is started with goes the same
// way.

// forwarded is what a second launcher sends the running one over the port.
// secret is the per-run capability from launcher.port: the listener accepts
// nothing without it, so a random local process (or a compromised renderer)
// cannot fire bundle tokens — which open napps and dispatch their actions —
// at the running launcher.
type forwarded struct {
	Token  string   `json:"token"`
	Secret string   `json:"secret"`
	Links  []string `json:"links,omitempty"`
}

func portFilePath(dataDir string) string {
	return filepath.Join(dataDir, "launcher.port")
}

// readForwardTarget reads the port and per-run secret out of launcher.port,
// which holds "<port>:<secret>". A file without the secret half is an older launcher:
// callers treat a missing secret as "forwarding will be refused".
func readForwardTarget(dataDir string) (int, string) {
	raw, err := os.ReadFile(portFilePath(dataDir))
	if err != nil {
		return 0, ""
	}
	portPart, secretPart, _ := strings.Cut(strings.TrimSpace(string(raw)), ":")
	port, err := strconv.Atoi(strings.TrimSpace(portPart))
	if err != nil || port <= 0 || port > 65535 {
		return 0, ""
	}
	return port, strings.TrimSpace(secretPart)
}

// forwardToInstance sends msg to a launcher already running, if there is
// one, answering true when it did (the caller should exit).
func forwardToInstance(dataDir string, msg forwarded) bool {
	port, secret := readForwardTarget(dataDir)
	if port == 0 || secret == "" {
		return false
	}
	msg.Secret = secret
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false // the launcher of that port is gone; we are the new one
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return false
	}
	answer, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || strings.TrimSpace(answer) != "ok" {
		// refused (a launcher from before this protocol, or a secret
		// mismatch): the caller starts a launcher of its own
		return false
	}
	return true
}

// startInstanceListener binds the forwarding port and writes the port file,
// so later verdana invocations have someone to talk to. Tokens arriving
// while the backend is still starting wait for it (see runBundleToken)
// instead of running into a launcher with no napp registry and no host.
func startInstanceListener(dataDir string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Warn().Err(err).Msg("no instance listener: shortcuts will start a new launcher")
		return
	}
	// the port file carries a per-run capability next to the port: the
	// listener refuses forwards without it. 0600, like the other files the
	// launcher writes for itself.
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		ln.Close()
		log.Warn().Err(err).Msg("no forwarding secret: shortcuts will start a new launcher")
		return
	}
	forwardSecret = hex.EncodeToString(secret)
	portFile := fmt.Sprintf("%d:%s", ln.Addr().(*net.TCPAddr).Port, forwardSecret)
	_ = os.MkdirAll(dataDir, 0700)
	if err := os.WriteFile(portFilePath(dataDir), []byte(portFile), 0600); err != nil {
		ln.Close()
		log.Warn().Err(err).Msg("could not write the launcher port file")
		return
	}

	go func() {
		defer ln.Close()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveForward(conn)
		}
	}()
}

func serveForward(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	var msg forwarded
	if err := json.NewDecoder(io.LimitReader(conn, maxForwardBytes)).Decode(&msg); err != nil {
		return
	}
	if subtle.ConstantTimeCompare([]byte(msg.Secret), []byte(forwardSecret)) != 1 {
		conn.Write([]byte("forbidden\n"))
		log.Warn().Msg("refused a forward without the run's secret")
		return
	}
	if msg.Token != "" {
		go runBundleToken(msg.Token)
	}
	for _, link := range msg.Links {
		go runLink(link)
	}
	conn.Write([]byte("ok\n"))
}

// launcherReady is closed by main once the backend is up. The listener takes
// tokens from the moment it binds its port, which is before that, so a token
// handed over while this launcher is still starting waits here instead of
// running into a backend that has no napp registry, no stores and no host to
// open windows with.
var launcherReady = make(chan struct{})

// forwardSecret is this run's forwarding capability, set by
// startInstanceListener before the listener accepts.
var forwardSecret string

// runBundleToken opens a bundle token's napps as soon as this launcher can.
// Each token gets its own goroutine, so a second shortcut click is never stuck
// behind a slow first one.
func runBundleToken(token string) {
	<-launcherReady
	if err := backend.RunShortcutToken(token); err != nil {
		log.Warn().Err(err).Str("token", previewToken(token)).Msg("bundle invocation failed")
		backend.SetFetchErr("shortcut failed: " + err.Error())
	}
}

// runLink opens a nostr: link as soon as this launcher can.
func runLink(link string) {
	<-launcherReady
	if err := backend.OpenLink(link); err != nil {
		log.Warn().Err(err).Str("link", link).Msg("link failed")
		backend.SetFetchErr(link + ": " + err.Error())
	}
}
