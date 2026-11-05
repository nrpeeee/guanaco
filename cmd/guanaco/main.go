package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"guanaco/web"
)

const version = "2.0.0"

type App struct {
	oll      *OllamaRef
	store    *Store
	rag      *ragStore
	mcp      *mcpHub
	reg      *Registry
	creds    *CredStore
	keyDir   string
	port     int
	allowLAN bool

	shutdownToken string       // required by /shutdown (see api.go)
	pageLoads     atomic.Int64 // counts page loads, to cancel reload-triggered exits
	tabs          tabSet       // open browser tabs, so closing one doesn't stop the others (tabs.go)
}

func main() {
	port := flag.Int("port", 3000, "port to listen on (tries the next few if busy)")
	ollama := flag.String("ollama", defaultOllama(), "Ollama base URL")
	dataDir := flag.String("data", "", "data directory (default: a \"data\" folder next to the executable if it exists, else a per-user folder; see README)")
	noBrowser := flag.Bool("no-browser", false, "don't open the browser on start")
	allowLAN := flag.Bool("lan", false, "listen on all interfaces (no auth! only on trusted networks; cloud providers are blocked)")
	lanCloud := flag.Bool("lan-cloud", false, "with -lan, also allow cloud providers (anyone on the network can spend your API credits)")
	flag.Parse()
	// An explicit -ollama flag or OLLAMA_HOST pins the URL: no auto-switching.
	pinned := os.Getenv("OLLAMA_HOST") != ""
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "ollama" {
			pinned = true
		}
	})

	if *dataDir == "" {
		exe, err := os.Executable()
		if err != nil {
			exe = "."
		}
		*dataDir = defaultDataDir(filepath.Dir(exe), runtime.GOOS, os.Getenv, os.UserHomeDir)
	}
	logPath := setupLog(*dataDir)
	store, err := NewStore(*dataDir)
	if err != nil {
		fatal("data dir %s: %v", *dataDir, err)
	}

	keyDir := filepath.Join(*dataDir, ".keys")
	if cfg, err := os.UserConfigDir(); err == nil {
		keyDir = filepath.Join(cfg, "Guanaco")
	}
	creds, err := NewCredStore(*dataDir, keyDir)
	if err != nil {
		fatal("credentials: %v", err)
	}
	oll := NewOllamaRef(*ollama)
	reg, err := NewRegistry(*dataDir, oll, creds, *allowLAN, *lanCloud)
	if err != nil {
		fatal("providers: %v", err)
	}
	token, err := randToken()
	if err != nil {
		fatal("shutdown token: %v", err)
	}
	rag, err := newRagStore(filepath.Join(*dataDir, "rag"))
	if err != nil {
		fatal("documents: %v", err)
	}
	hub, err := newMCPHub(*dataDir)
	if err != nil {
		fatal("tools: %v", err)
	}
	app := &App{oll: oll, store: store, rag: rag, mcp: hub, reg: reg, creds: creds, keyDir: keyDir, allowLAN: *allowLAN, shutdownToken: token}

	host := "127.0.0.1"
	if *allowLAN {
		host = "0.0.0.0"
	}
	var ln net.Listener
	for p := *port; p < *port+10; p++ {
		ln, err = net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p)))
		if err == nil {
			app.port = p
			break
		}
	}
	if ln == nil {
		fatal("could not bind a port starting at %d: %v", *port, err)
	}

	srv := &http.Server{Handler: app.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	url := fmt.Sprintf("http://localhost:%d", app.port)

	fmt.Printf("\n  Guanaco %s\n  ─────────────────────────────\n  UI      %s\n  Ollama  %s\n  Data    %s\n", version, url, app.oll.Get(), *dataDir)
	if logPath != "" {
		fmt.Printf("  Log     %s\n", logPath)
	}
	// Also goes to the log file, so a windowless start leaves a trace of the
	// port it picked.
	log.Printf("Guanaco %s on %s (data %s, lan %v)", version, url, *dataDir, *allowLAN)
	if *allowLAN {
		fmt.Println("  WARNING listening on all interfaces with no authentication")
		if !*lanCloud {
			fmt.Println("          cloud providers are blocked in this mode")
		}
	}
	fmt.Println("\n  Press Ctrl+C to quit.")

	if !pinned {
		go app.autodetect()
	}
	if !*noBrowser {
		go func() { time.Sleep(300 * time.Millisecond); openBrowser(url) }()
	}
	if err := srv.Serve(ln); err != nil {
		fatal("server: %v", err)
	}
}

func defaultOllama() string {
	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		if !strings.HasPrefix(h, "http") {
			h = "http://" + h
		}
		h = strings.Replace(h, "0.0.0.0", "127.0.0.1", 1)
		return h
	}
	return "http://127.0.0.1:11434"
}

func init() {
	// Module scripts are refused unless served with a JavaScript MIME type, and
	// on Windows the registry can map .js to text/plain. Don't depend on it.
	mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	mime.AddExtensionType(".css", "text/css; charset=utf-8")
}

func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	a.routes(mux)
	mux.Handle("/", http.FileServer(http.FS(web.FS)))
	return a.guard(a.countPageLoads(mux))
}

// countPageLoads lets /shutdown tell a reload (a fresh page load follows the
// close beacon) from a real tab close, so F5 doesn't kill the server.
func (a *App) countPageLoads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/index.html") {
			a.pageLoads.Add(1)
		}
		next.ServeHTTP(w, r)
	})
}

// guard blocks DNS-rebinding (Host check) and cross-site requests to the API
// (custom header forces a CORS preflight we never answer).
func (a *App) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.allowLAN {
			h := r.Host
			if hh, _, err := net.SplitHostPort(h); err == nil {
				h = hh
			}
			if h != "localhost" && h != "127.0.0.1" && h != "::1" {
				http.Error(w, "forbidden host", http.StatusForbidden)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get("X-Guanaco") != "1" {
			http.Error(w, "missing X-Guanaco header", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// setupLog tees output to <data>/guanaco.log so a -H=windowsgui build (which has
// no console) still records why it failed. Returns the path, or "" on failure.
func setupLog(dataDir string) string {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return ""
	}
	path := filepath.Join(dataDir, "guanaco.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return ""
	}
	// File first: under windowsgui stderr is an invalid handle and a failed
	// write there must not stop the file from being written.
	log.SetOutput(io.MultiWriter(f, ignoreErr{os.Stderr}))
	return path
}

type ignoreErr struct{ w io.Writer }

func (w ignoreErr) Write(p []byte) (int, error) {
	w.w.Write(p) // best effort: a broken stderr must not break the log
	return len(p), nil
}

// fatal reports a startup failure somewhere a windowless build can see it:
// the log file and, on Windows, a message box.
func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	notifyFatal(msg)
	os.Exit(1)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
