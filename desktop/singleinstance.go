package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"verdana/backend"
)

// Single-instance forwarding: the first launcher binds a localhost port and
// writes the number to launcher.port in its data dir. Later invocations hand a
// command to that process and exit: a plain launch opens the manager while a
// shortcut launch opens its napps.

type instanceCommand struct {
	Command string `json:"command"`
	Token   string `json:"token,omitempty"`
}

const (
	commandOpenManager   = "open-manager"
	commandRunShortcut   = "run-shortcut"
	commandEnsureRunning = "ensure-running"
)

func portFilePath(dataDir string) string {
	return filepath.Join(dataDir, "launcher.port")
}

func readPort(dataDir string) int {
	raw, err := os.ReadFile(portFilePath(dataDir))
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

// forwardToInstance sends a command to a launcher already running, if there
// is one, answering true when it did (the caller should exit).
func forwardToInstance(dataDir string, msg instanceCommand) bool {
	port := readPort(dataDir)
	if port == 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false // the launcher of that port is gone; we are the new one
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return false
	}
	if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
		return false
	}
	return true
}

// startInstanceListener binds the forwarding port and writes the port file,
// so later verdana invocations have someone to talk to. Tokens arriving
// while the backend is still starting wait for it (see runBundleToken)
// instead of running into a launcher with no napp registry and no host.
func startInstanceListener(dataDir string) func() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Warn().Err(err).Msg("no instance listener: shortcuts will start a new launcher")
		return func() {}
	}
	if err := os.WriteFile(portFilePath(dataDir), []byte(strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)), 0644); err != nil {
		log.Warn().Err(err).Msg("could not write the launcher port file")
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveForward(conn)
		}
	}()
	return func() {
		ln.Close()
		os.Remove(portFilePath(dataDir))
	}
}

func serveForward(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	var msg instanceCommand
	if err := json.NewDecoder(conn).Decode(&msg); err != nil {
		return
	}
	// Accept the token-only message used by older Verdana binaries during a
	// rolling upgrade.
	if msg.Command == "" && msg.Token != "" {
		msg.Command = commandRunShortcut
	}
	go runInstanceCommand(msg)
	conn.Write([]byte("ok\n"))
}

// launcherReady is closed by main once the backend is up. The listener takes
// tokens from the moment it binds its port, which is before that, so a token
// handed over while this launcher is still starting waits here instead of
// running into a backend that has no napp registry, no stores and no host to
// open windows with.
var launcherReady = make(chan struct{})

// runBundleToken opens a bundle token's napps as soon as this launcher can.
// Each token gets its own goroutine, so a second shortcut click is never stuck
// behind a slow first one.
func runBundleToken(token string) {
	<-launcherReady
	runBundleTokenReady(token)
}

func runInstanceCommand(msg instanceCommand) {
	<-launcherReady
	switch msg.Command {
	case commandOpenManager:
		showManager()
	case commandRunShortcut:
		runBundleTokenReady(msg.Token)
	case commandEnsureRunning:
		// The caller only wanted to make sure the background process exists.
	default:
		log.Warn().Str("command", msg.Command).Msg("unknown instance command")
	}
}

func runBundleTokenReady(token string) {
	if err := backend.RunShortcutToken(token); err != nil {
		log.Warn().Err(err).Str("token", previewToken(token)).Msg("bundle invocation failed")
		backend.SetFetchErr("shortcut failed: " + err.Error())
	}
}
