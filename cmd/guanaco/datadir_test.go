package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	exe := t.TempDir()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	home := func() (string, error) { return filepath.FromSlash("/home/u"), nil }
	noHome := func() (string, error) { return "", errors.New("no home") }
	xdg := filepath.Join(t.TempDir(), "share")

	cases := []struct {
		name, goos string
		env        map[string]string
		home       func() (string, error)
		want       string
	}{
		{"windows local appdata", "windows", map[string]string{"LocalAppData": `C:\Users\u\AppData\Local`}, home, filepath.Join(`C:\Users\u\AppData\Local`, "Guanaco")},
		{"windows roaming fallback", "windows", map[string]string{"AppData": `C:\Users\u\AppData\Roaming`}, home, filepath.Join(`C:\Users\u\AppData\Roaming`, "Guanaco")},
		{"windows nothing", "windows", nil, home, filepath.Join(exe, "data")},
		{"linux default", "linux", nil, home, filepath.Join(filepath.FromSlash("/home/u"), ".local", "share", "guanaco")},
		// XDG_DATA_HOME must be absolute *on the machine running the test*:
		// "/x/share" has no drive letter, so it isn't absolute on Windows.
		{"linux xdg", "linux", map[string]string{"XDG_DATA_HOME": xdg}, home, filepath.Join(xdg, "guanaco")},
		{"linux relative xdg ignored", "linux", map[string]string{"XDG_DATA_HOME": "rel"}, home, filepath.Join(filepath.FromSlash("/home/u"), ".local", "share", "guanaco")},
		{"linux no home", "linux", nil, noHome, filepath.Join(exe, "data")},
		{"darwin", "darwin", nil, home, filepath.Join(filepath.FromSlash("/home/u"), "Library", "Application Support", "Guanaco", "data")},
	}
	for _, c := range cases {
		if got := defaultDataDir(exe, c.goos, env(c.env), c.home); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// An existing ./data next to the executable always wins (portable / upgrade).
	if err := os.Mkdir(filepath.Join(exe, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := defaultDataDir(exe, "linux", env(nil), home); got != filepath.Join(exe, "data") {
		t.Errorf("existing portable data dir should win, got %q", got)
	}
}
