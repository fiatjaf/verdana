package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync"

	"fiatjaf.com/nostr"
)

type NappPath struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

type Napp struct {
	ID          string          `json:"id"`
	D           string          `json:"d"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Icon        string          `json:"icon"`
	Author      string          `json:"author"`
	Actions     []string        `json:"actions"`
	CreatedAt   nostr.Timestamp `json:"created_at"`
	Paths       []NappPath      `json:"paths"`
	Servers     []string        `json:"servers"`
}

type AppState struct {
	ClientKey      nostr.SecretKey `json:"client_key"`
	Login          string          `json:"login"`
	Relays         []string        `json:"relays"`
	InstalledNapps map[string]Napp `json:"installed_napps"`
}

type uiState struct {
	mu        sync.Mutex
	phase     string
	tab       int
	loginErr  string
	profName  string
	profPic   string
	fetchErr  string
	fetching  bool
	discovery []Napp
	installed []Napp
	busy      map[string]bool
}

type openReq struct {
	napp Napp
	dir  string
}

type wireMsg struct {
	T      string          `json:"t"`
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params string          `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`
}

type childInfo struct {
	cmd   *exec.Cmd
	enc   *json.Encoder
	encMu sync.Mutex
	subs  map[int]context.CancelFunc
	subMu sync.Mutex
}
