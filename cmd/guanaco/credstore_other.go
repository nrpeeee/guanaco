//go:build !windows

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// AES-256-GCM. The 32-byte key lives in the OS config dir (e.g.
// ~/.config/Guanaco/credentials.key, mode 0600), NOT next to the data, so a
// copied/backed-up/shared Guanaco-data folder doesn't carry usable keys.

const keyName = "credentials.key"

func loadOrCreateKey(dir string, create bool) ([]byte, error) {
	p := filepath.Join(dir, keyName)
	k, err := readKey(p)
	if err == nil {
		return k, nil
	}
	if !create || !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	k = make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	// Write the key to a private temp file, then publish it with link(2), which
	// fails if the name exists. Readers therefore never see a half-written key,
	// and if another process won the race we use its key instead of replacing it
	// (which would orphan anything it had already sealed).
	tmp, err := os.CreateTemp(dir, keyName+".new-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(k); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	switch err := os.Link(tmp.Name(), p); {
	case err == nil:
		return k, nil
	case errors.Is(err, os.ErrExist):
		return readKey(p)
	default: // filesystem without hard links: fall back to rename, unless someone beat us to it
		if _, statErr := os.Stat(p); statErr == nil {
			return readKey(p)
		}
		if err := os.Rename(tmp.Name(), p); err != nil {
			return nil, err
		}
		return k, nil
	}
}

func readKey(p string) ([]byte, error) {
	k, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if len(k) != 32 {
		return nil, errors.New("credential key file is corrupt")
	}
	return k, nil
}

func gcmFor(dir string, create bool) (cipher.AEAD, error) {
	k, err := loadOrCreateKey(dir, create)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

var aad = []byte("Guanaco-credentials-v1")

func seal(plain []byte, dir string) ([]byte, error) {
	g, err := gcmFor(dir, true)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, aad), nil
}

func unseal(sealed []byte, dir string) ([]byte, error) {
	g, err := gcmFor(dir, false)
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, errors.New("credentials file is truncated")
	}
	return g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], aad)
}

func credProtection(dir string) string {
	return "Encrypted with AES-256-GCM; the key is stored separately in " + filepath.Join(dir, keyName) + " (readable only by you)."
}
