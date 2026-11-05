package main

import (
	"net/http"
	"regexp"
	"sync"
	"time"
)

// Open-tab tracking for the close-the-tab-to-quit behaviour.
//
// Every page sends POST /api/tabs/alive with its own random id when it loads and
// then on a timer. When a tab closes it sends the /shutdown beacon with that id.
// The server exits only once no other tab is still alive, so closing one of two
// tabs no longer kills the other.
//
// Background tabs have their timers throttled (Chrome: down to once a minute),
// so "alive" is judged generously: a tab counts until it has been silent for
// tabTTL. After a beacon the server keeps checking until either another tab
// proves it is alive (a heartbeat newer than the beacon), the page is loaded
// again (a reload), or every other tab has gone quiet for tabTTL.

const (
	tabTTL = 150 * time.Second
	tabMax = 64 // ignore heartbeats beyond this many tabs
)

// Variables so tests can shorten them.
var (
	tabRecheck   = 5 * time.Second
	reloadWindow = 1500 * time.Millisecond
)

var reTabID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type tabSet struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (t *tabSet) alive(id string, now time.Time) bool {
	if !reTabID.MatchString(id) {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen == nil {
		t.seen = map[string]time.Time{}
	}
	if _, ok := t.seen[id]; !ok && len(t.seen) >= tabMax {
		t.pruneLocked(now)
		if len(t.seen) >= tabMax {
			return false
		}
	}
	t.seen[id] = now
	return true
}

func (t *tabSet) closed(id string) {
	t.mu.Lock()
	delete(t.seen, id)
	t.mu.Unlock()
}

func (t *tabSet) pruneLocked(now time.Time) {
	for id, at := range t.seen {
		if now.Sub(at) > tabTTL {
			delete(t.seen, id)
		}
	}
}

// verdict decides what to do some time after a tab-close beacon sent at since:
// exit when no tab is left, stop waiting when another tab has checked in since
// the beacon, otherwise keep waiting for silent tabs to time out.
func (t *tabSet) verdict(since, now time.Time) (exit, stillOpen bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
	for _, at := range t.seen {
		if at.After(since) {
			return false, true
		}
	}
	return len(t.seen) == 0, false
}

func (a *App) tabAlive(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, 1<<10, &in) {
		return
	}
	if !a.tabs.alive(in.ID, time.Now()) {
		jsonErr(w, 400, "bad tab id")
		return
	}
	w.WriteHeader(204)
}

// waitForExit runs after a valid shutdown beacon. exitFn is os.Exit in
// production; tests pass their own.
func (a *App) waitForExit(since time.Time, loads int64, exitFn func()) {
	time.Sleep(reloadWindow)
	for {
		if a.pageLoads.Load() != loads {
			return // the page was loaded again: a reload, or a new tab
		}
		exit, stillOpen := a.tabs.verdict(since, time.Now())
		if stillOpen {
			return
		}
		if exit {
			exitFn()
			return
		}
		time.Sleep(tabRecheck)
	}
}
