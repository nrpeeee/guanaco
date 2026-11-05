package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"guanaco/web"
)

var reImport = regexp.MustCompile(`(?m)^import\s[^;]*?from\s+'(\./[^']+)';`)

// Every relative import in the UI's ES modules must point at a file that is
// actually embedded, and index.html must load the entry module.
func TestEmbeddedModulesResolve(t *testing.T) {
	html, err := fs.ReadFile(web.FS, "index.html")
	if err != nil || !strings.Contains(string(html), `<script type="module" src="js/main.js">`) {
		t.Fatalf("index.html must load js/main.js as a module (err=%v)", err)
	}
	files, err := fs.Glob(web.FS, "js/*.js")
	if err != nil || len(files) < 10 {
		t.Fatalf("expected the UI modules to be embedded, got %v (err=%v)", files, err)
	}
	for _, f := range files {
		src, _ := fs.ReadFile(web.FS, f)
		for _, m := range reImport.FindAllStringSubmatch(string(src), -1) {
			if _, err := fs.Stat(web.FS, "js/"+strings.TrimPrefix(m[1], "./")); err != nil {
				t.Errorf("%s imports %s, which is not embedded", f, m[1])
			}
		}
	}
}

// With X-Content-Type-Options: nosniff the browser refuses module scripts that
// aren't served as JavaScript.
func TestScriptsServedWithJavaScriptMIME(t *testing.T) {
	a := &App{}
	srv := httptest.NewServer(a.handler())
	defer srv.Close()
	for _, p := range []string{"/js/main.js", "/theme-init.js", "/vendor/marked.min.js"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		ct := resp.Header.Get("Content-Type")
		if resp.StatusCode != 200 || !strings.HasPrefix(ct, "text/javascript") {
			t.Errorf("%s: status %d, content-type %q", p, resp.StatusCode, ct)
		}
	}
}
