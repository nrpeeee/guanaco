//go:build !windows

package main

import (
	"bytes"
	"sync"
	"testing"
)

// Several processes (here: goroutines) creating the key at once must all end
// up with the same key, not each overwrite the file with their own.
func TestKeyCreationRace(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	keys := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			keys[i], errs[i] = loadOrCreateKey(dir, true)
		}(i)
	}
	close(start)
	wg.Wait()
	final, err := loadOrCreateKey(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := range keys {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if !bytes.Equal(keys[i], final) {
			t.Fatalf("goroutine %d got a key that is not the one on disk", i)
		}
	}
}
