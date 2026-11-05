package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// CredStore holds provider API keys, separately from settings.json and chat
// files. The whole map is encrypted at rest:
//   - Windows: DPAPI (CryptProtectData), bound to your Windows user account.
//   - elsewhere: AES-256-GCM with a random key kept outside the data folder
//     (in the OS config dir, mode 0600).
//
// Keys are write-only from the browser's point of view: the API never returns
// them, only whether one is set and its last 4 characters.
type CredStore struct {
	path   string
	keyDir string
	mu     sync.Mutex
	m      map[string]string
	broken bool // existing file couldn't be decrypted (e.g. copied from another user)
}

const credFile = "credentials.enc"

func NewCredStore(dataDir, keyDir string) (*CredStore, error) {
	s := &CredStore{path: filepath.Join(dataDir, credFile), keyDir: keyDir, m: map[string]string{}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	plain, err := unseal(b, keyDir)
	if err == nil {
		err = json.Unmarshal(plain, &s.m)
	}
	if err != nil {
		log.Printf("credentials: cannot decrypt %s (%v) — stored API keys are unavailable; re-enter them under Providers", s.path, err)
		s.broken = true
		s.m = map[string]string{}
	}
	return s, nil
}

func (s *CredStore) Get(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.m[id]
	return k, ok
}

func (s *CredStore) Set(id, key string) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 1024 || strings.ContainsAny(key, "\r\n\x00") {
		return errors.New("invalid API key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.m[id]
	s.m[id] = key
	if err := s.saveLocked(); err != nil {
		if had { // keep memory identical to what is on disk
			s.m[id] = prev
		} else {
			delete(s.m, id)
		}
		return err
	}
	return nil
}

func (s *CredStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.m[id]
	if !ok {
		return nil
	}
	delete(s.m, id)
	if err := s.saveLocked(); err != nil {
		s.m[id] = prev
		return err
	}
	return nil
}

func (s *CredStore) saveLocked() error {
	if s.broken { // keep the unreadable original around instead of clobbering it
		_ = os.Rename(s.path, s.path+".unreadable.bak")
		s.broken = false
	}
	if len(s.m) == 0 {
		err := os.Remove(s.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	plain, _ := json.Marshal(s.m)
	sealed, err := seal(plain, s.keyDir)
	if err != nil {
		return err
	}
	return writeAtomic(s.path, sealed)
}

// Hint is safe to show in the UI.
func keyHint(k string) string {
	if len(k) <= 8 {
		return "••••"
	}
	return "••••" + k[len(k)-4:]
}

func randID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error()) // never fall back to predictable IDs
	}
	return hex.EncodeToString(b)
}

// randToken is a longer secret than randID: it authenticates /shutdown.
func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
