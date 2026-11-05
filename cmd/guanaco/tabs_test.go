package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestTabVerdict(t *testing.T) {
	var ts tabSet
	t0 := time.Now()
	if !ts.alive("tab-aaaaaaaa", t0) || !ts.alive("tab-bbbbbbbb", t0) {
		t.Fatal("valid ids rejected")
	}
	if ts.alive("x", t0) || ts.alive("bad id!!", t0) {
		t.Fatal("invalid ids accepted")
	}

	// Tab A closes; B is still around but hasn't checked in since: keep waiting.
	ts.closed("tab-aaaaaaaa")
	beacon := t0.Add(time.Second)
	if exit, open := ts.verdict(beacon, beacon.Add(2*time.Second)); exit || open {
		t.Fatalf("got exit=%v open=%v, want keep waiting", exit, open)
	}
	// B heartbeats after the beacon: it's open, stop waiting.
	ts.alive("tab-bbbbbbbb", beacon.Add(10*time.Second))
	if exit, open := ts.verdict(beacon, beacon.Add(11*time.Second)); exit || !open {
		t.Fatalf("got exit=%v open=%v, want still open", exit, open)
	}
	// B goes silent long enough to be considered gone (a crash, no beacon): exit.
	late := beacon.Add(10*time.Second + tabTTL + time.Second)
	if exit, _ := ts.verdict(late, late); !exit {
		t.Fatal("expected exit once every tab timed out")
	}
	// Last tab closing with nothing else open: exit.
	var one tabSet
	one.alive("tab-cccccccc", t0)
	one.closed("tab-cccccccc")
	if exit, _ := one.verdict(t0, t0); !exit {
		t.Fatal("expected exit when the only tab closes")
	}
}

func TestTabMax(t *testing.T) {
	var ts tabSet
	now := time.Now()
	for i := 0; i < tabMax; i++ {
		if !ts.alive("tab-"+string(rune('a'+i%26))+string(rune('a'+i/26))+"xxxxxx", now) {
			t.Fatalf("tab %d rejected", i)
		}
	}
	if ts.alive("tab-overflow-1", now) {
		t.Fatal("accepted more than tabMax tabs")
	}
	if !ts.alive("tab-overflow-1", now.Add(tabTTL+time.Second)) {
		t.Fatal("stale tabs should be pruned to make room")
	}
}

func TestWaitForExit(t *testing.T) {
	defer func(r, c time.Duration) { reloadWindow, tabRecheck = r, c }(reloadWindow, tabRecheck)
	reloadWindow, tabRecheck = 10*time.Millisecond, 10*time.Millisecond

	run := func(a *App, since time.Time, during func()) bool {
		var exited atomic.Bool
		done := make(chan struct{})
		loads := a.pageLoads.Load()
		go func() { a.waitForExit(since, loads, func() { exited.Store(true) }); close(done) }()
		if during != nil {
			during()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("waitForExit did not return")
		}
		return exited.Load()
	}

	// Only tab closed: exits.
	a := &App{}
	if !run(a, time.Now(), nil) {
		t.Error("expected exit with no tabs open")
	}
	// Reload: the page is fetched again within the window, no exit.
	a = &App{}
	a.tabs.alive("tab-aaaaaaaa", time.Now())
	if run(a, time.Now(), func() { a.pageLoads.Add(1) }) {
		t.Error("reload must not exit")
	}
	// Another tab checks in after the beacon: no exit.
	a = &App{}
	since := time.Now()
	a.tabs.alive("tab-bbbbbbbb", since.Add(-time.Second))
	if run(a, since, func() { time.Sleep(20 * time.Millisecond); a.tabs.alive("tab-bbbbbbbb", time.Now()) }) {
		t.Error("an open tab must keep the server running")
	}
}
