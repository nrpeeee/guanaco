package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
)

// Store keeps each chat as a plain JSON file: <data>/chats/<id>.json, and
// global settings in <data>/settings.json. Easy to back up, grep, or delete.
type Store struct {
	dir string
	wmu sync.Mutex // serializes writers only: writes are rename-based, so reads are lock-free
}

type ChatMeta struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Model    string `json:"model"`
	Created  int64  `json:"created"`
	Updated  int64  `json:"updated"`
	Pinned   bool   `json:"pinned"`
	Provider string `json:"provider,omitempty"`
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "chats"), 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) chatPath(id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", errors.New("bad id")
	}
	return filepath.Join(s.dir, "chats", id+".json"), nil
}

func (s *Store) List() ([]ChatMeta, error) {
	files, err := filepath.Glob(filepath.Join(s.dir, "chats", "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]ChatMeta, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var m ChatMeta
		if json.Unmarshal(b, &m) == nil && m.ID != "" {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out, nil
}

// Reads never take the lock: writeAtomic replaces files by rename, so a reader
// always sees either the old or the new complete file. Holding the write lock
// here used to block List/Get behind a slow 100 MB chat save.
func (s *Store) Get(id string) ([]byte, error) {
	p, err := s.chatPath(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

func (s *Store) Put(id string, raw []byte) error {
	p, err := s.chatPath(id)
	if err != nil {
		return err
	}
	var m ChatMeta
	if err := json.Unmarshal(raw, &m); err != nil || m.ID != id {
		return errors.New("invalid chat body")
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return writeAtomic(p, raw)
}

func (s *Store) Delete(id string) error {
	p, err := s.chatPath(id)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) GetSettings() []byte {
	b, err := os.ReadFile(filepath.Join(s.dir, "settings.json"))
	if err != nil || !json.Valid(b) {
		return []byte("{}")
	}
	return b
}

func (s *Store) PutSettings(raw []byte) error {
	if !json.Valid(raw) {
		return errors.New("invalid json")
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return writeAtomic(filepath.Join(s.dir, "settings.json"), raw)
}

// writeAtomic replaces path with data so that readers see either the old or
// the new complete file, never a partial one. The data is flushed to disk
// before the rename, so a crash or power cut can't leave an empty file behind.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = replaceFile(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
