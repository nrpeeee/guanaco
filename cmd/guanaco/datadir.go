package main

import (
	"os"
	"path/filepath"
)

// defaultDataDir picks where chats, settings and documents live when -data is
// not given:
//
//  1. a "data" folder next to the executable, if one already exists (portable
//     installs, and anyone upgrading from an older version);
//  2. otherwise a per-user folder, so the data is not buried in the install
//     location (e.g. ~/.local/bin) or lost when the program is rebuilt/moved:
//     Windows  %LocalAppData%\Guanaco
//     macOS    ~/Library/Application Support/Guanaco/data
//     Linux    $XDG_DATA_HOME/guanaco  (default ~/.local/share/guanaco)
//
// If no per-user location can be determined it falls back to ./data next to
// the executable.
func defaultDataDir(exeDir, goos string, getenv func(string) string, home func() (string, error)) string {
	portable := filepath.Join(exeDir, "data")
	if st, err := os.Stat(portable); err == nil && st.IsDir() {
		return portable
	}
	switch goos {
	case "windows":
		if d := getenv("LocalAppData"); d != "" {
			return filepath.Join(d, "Guanaco")
		}
		if d := getenv("AppData"); d != "" {
			return filepath.Join(d, "Guanaco")
		}
	case "darwin":
		if h, err := home(); err == nil && h != "" {
			return filepath.Join(h, "Library", "Application Support", "Guanaco", "data")
		}
	default:
		if d := getenv("XDG_DATA_HOME"); d != "" && filepath.IsAbs(d) {
			return filepath.Join(d, "guanaco")
		}
		if h, err := home(); err == nil && h != "" {
			return filepath.Join(h, ".local", "share", "guanaco")
		}
	}
	return portable
}
