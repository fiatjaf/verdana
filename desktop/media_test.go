//go:build !windows

package main

import (
	"bufio"
	"net"
	"strconv"
	"testing"

	"verdana/backend"
)

func stateString(st backend.MediaState) string {
	f := func(p *float64) string {
		if p == nil {
			return "-"
		}
		return strconv.FormatFloat(*p, 'g', -1, 64)
	}
	return st.Status + " " + f(st.Position) + " " + f(st.Duration) + " " + f(st.Volume)
}

func TestMpvStateFromEvents(t *testing.T) {
	var s mpvState
	steps := []struct {
		line    string
		changed bool
		want    string
	}{
		{`{"request_id":0,"error":"success"}`, false, "buffering - - -"},
		{`{"event":"property-change","id":1,"name":"pause","data":false}`, true, "buffering - - -"},
		{`{"event":"property-change","id":3,"name":"duration","data":20.000000}`, true, "buffering - 20 -"},
		{`{"event":"property-change","id":4,"name":"volume","data":50.000000}`, true, "buffering - 20 0.5"},
		{`{"event":"property-change","id":2,"name":"time-pos","data":1.5}`, true, "playing 1.5 20 0.5"},
		{`{"event":"property-change","id":5,"name":"paused-for-cache","data":true}`, true, "buffering 1.5 20 0.5"},
		{`{"event":"property-change","id":5,"name":"paused-for-cache","data":false}`, true, "playing 1.5 20 0.5"},
		{`{"event":"property-change","id":1,"name":"pause","data":true}`, true, "paused 1.5 20 0.5"},
		{`{"event":"property-change","id":4,"name":"volume","data":130}`, true, "paused 1.5 20 1"},
		{`{"event":"seek"}`, false, "paused 1.5 20 1"},
		{`{"event":"property-change","id":2,"name":"time-pos","data":null}`, true, "paused - 20 1"},
		{`{"event":"property-change","id":6,"name":"eof-reached","data":true}`, true, "stopped - 20 1"},
		{`not json`, false, "stopped - 20 1"},
	}
	for _, step := range steps {
		if got := s.apply([]byte(step.line)); got != step.changed {
			t.Errorf("%s: changed = %v", step.line, got)
		}
		if got := stateString(s.media()); got != step.want {
			t.Errorf("%s: state %q, want %q", step.line, got, step.want)
		}
	}
}

func TestVLCStateFromOutput(t *testing.T) {
	var s vlcState
	s.pending = []string{"get_time", "get_length", "get_time", "get_length"}
	steps := []struct {
		line    string
		changed bool
		want    string
	}{
		{"status change: ( new input: https://example.com/a.ogg )\r", false, "buffering - - -"},
		{"status change: ( play state: 3 )\r", true, "playing - - -"},
		{"> 1\r", true, "playing 1 - -"},
		{"20\r", true, "playing 1 20 -"},
		{"status change: ( audio volume: 128 )", true, "playing 1 20 0.5"},
		{"volume: returned 0 (no error)", false, "playing 1 20 0.5"},
		{"pause: returned 0 (no error)", false, "playing 1 20 0.5"},
		{"status change: ( pause state: 3 ): Pause", true, "playing 1 20 0.5"},
		{"status change: ( pause state: 4 )", true, "paused 1 20 0.5"},
		{"Type 'pause' to continue.", false, "paused 1 20 0.5"},
		{"1", false, "paused 1 20 0.5"},
		{"0", true, "paused 1 - 0.5"},
		{"7", false, "paused 1 - 0.5"}, // nothing pending: not an answer
		{"status change: ( play state: 2 ): Play", true, "playing 1 - 0.5"},
		{"status change: ( play state: 4 ): End", true, "stopped 1 - 0.5"},
		{"status change: ( play state: 3 )", true, "playing 1 - 0.5"},
		{"status change: ( stop state: 5 )", true, "stopped 1 - 0.5"},
	}
	for _, step := range steps {
		if got := s.apply(step.line); got != step.changed {
			t.Errorf("%q: changed = %v", step.line, got)
		}
		if got := stateString(s.media()); got != step.want {
			t.Errorf("%q: state %q, want %q", step.line, got, step.want)
		}
	}
}

func TestMpvReplaceHandsThePlayerOver(t *testing.T) {
	ours, theirs := net.Pipe()
	defer ours.Close()
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(theirs)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	var got []string
	p := &mpvPlayer{proc: &playerProc{done: make(chan struct{})}, conn: ours}
	first := &mpvSession{p: p, onState: func(st backend.MediaState) { got = append(got, "first:"+st.Status) }}
	p.owner = first
	p.state.vol = fptr(0.5)

	states := make(chan backend.MediaState, 4)
	mp := p.replace(backend.MediaRequest{URL: "https://example.com/b.mp3", Title: "B", Autoplay: true},
		func(st backend.MediaState) { states <- st })
	if mp == nil {
		t.Fatal("replace refused a running mpv")
	}
	for _, want := range []string{
		`{"command":["set_property","pause",false]}`,
		`{"command":["set_property","force-media-title","B"]}`,
		`{"command":["loadfile","https://example.com/b.mp3","replace"]}`,
	} {
		if line := <-lines; line != want {
			t.Fatalf("sent %s, want %s", line, want)
		}
	}
	if st := <-states; st.Status != "buffering" || st.Volume == nil || *st.Volume != 0.5 {
		t.Fatalf("first state after replace: %s", stateString(st))
	}

	// the replaced session is cut off: its commands go nowhere and it hears
	// nothing more, while the new one steers mpv
	first.Pause()
	first.Stop()
	mp.Pause()
	if line := <-lines; line != `{"command":["set_property","pause",true]}` {
		t.Fatalf("sent %s after the new session paused", line)
	}
	p.emit(backend.MediaState{Status: "stopped"})
	if st := <-states; st.Status != "stopped" {
		t.Fatalf("new session got %s", stateString(st))
	}
	if len(got) != 0 {
		t.Fatalf("replaced session still heard %v", got)
	}

	close(p.proc.done)
	if p.replace(backend.MediaRequest{URL: "https://example.com/c.mp3"}, func(backend.MediaState) {}) != nil {
		t.Fatal("replace took an mpv that has exited")
	}
}
