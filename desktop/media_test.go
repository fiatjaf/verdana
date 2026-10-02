//go:build !windows

package main

import (
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
