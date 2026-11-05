package main

import (
	"archive/zip"
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// blockPath makes path an unremovable non-empty directory, so the atomic
// rename that writeAtomic ends with fails — a stand-in for "disk full /
// read-only" that also works when the tests run as root.
func blockPath(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestCredStoreRoundTrip(t *testing.T) {
	data, keys := t.TempDir(), t.TempDir()
	s, err := NewCredStore(data, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("openai", "  sk-test-1234567890  "); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(data, credFile))
	if len(raw) == 0 || string(raw) == "" || contains(raw, "sk-test") {
		t.Fatal("credentials file must exist and must not contain the key in clear text")
	}
	s2, err := NewCredStore(data, keys)
	if err != nil {
		t.Fatal(err)
	}
	if k, ok := s2.Get("openai"); !ok || k != "sk-test-1234567890" {
		t.Fatalf("reloaded key = %q, %v", k, ok)
	}
	if err := s2.Delete("openai"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, credFile)); !os.IsNotExist(err) {
		t.Fatal("deleting the last key should remove the file")
	}
}

func contains(b []byte, sub string) bool { return len(sub) > 0 && indexOf(string(b), sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestCredStoreRejectsBadKeys(t *testing.T) {
	s, _ := NewCredStore(t.TempDir(), t.TempDir())
	for _, k := range []string{"", "   ", "a\nb", "a\x00b"} {
		if s.Set("x", k) == nil {
			t.Errorf("key %q should be rejected", k)
		}
	}
}

func TestCredStoreRollsBackOnFailedSave(t *testing.T) {
	data := t.TempDir()
	s, _ := NewCredStore(data, t.TempDir())
	if err := s.Set("a", "key-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	// Break persistence, then try to change state.
	os.Remove(filepath.Join(data, credFile))
	blockPath(t, filepath.Join(data, credFile))

	if err := s.Set("a", "key-changed!"); err == nil {
		t.Fatal("expected the save to fail")
	}
	if k, _ := s.Get("a"); k != "key-aaaaaaaa" {
		t.Fatalf("after failed update the in-memory key is %q, want the old one", k)
	}
	if err := s.Set("b", "key-bbbbbbbb"); err == nil {
		t.Fatal("expected the save to fail")
	}
	if _, ok := s.Get("b"); ok {
		t.Fatal("a key that failed to save must not stay in memory")
	}
	if err := s.Delete("a"); err == nil {
		t.Fatal("expected the save to fail")
	}
	if _, ok := s.Get("a"); !ok {
		t.Fatal("a delete that failed to save must not remove the key from memory")
	}
}

func TestRegistryRollsBackOnFailedSave(t *testing.T) {
	data := t.TempDir()
	creds, _ := NewCredStore(data, t.TempDir())
	r, err := NewRegistry(data, NewOllamaRef("http://127.0.0.1:11434"), creds, false, false)
	if err != nil {
		t.Fatal(err)
	}
	before := len(r.Configs())
	blockPath(t, filepath.Join(data, "providers.json"))

	if _, err := r.Add(ProviderConfig{Kind: "openai-compatible", Name: "x", BaseURL: "http://127.0.0.1:1/v1", Enabled: true}); err == nil {
		t.Fatal("Add should report the failed save")
	}
	if len(r.Configs()) != before {
		t.Fatal("a provider that failed to save must not stay in the running config")
	}
	_, err = r.Update("ollama", func(c *ProviderConfig) error { c.Name = "renamed"; return nil })
	if err == nil {
		t.Fatal("Update should report the failed save")
	}
	if c, _ := r.Config("ollama"); c.Name == "renamed" {
		t.Fatal("a failed Update must be rolled back")
	}
}

func TestMCPHubRollsBackOnFailedSave(t *testing.T) {
	dir := t.TempDir()
	h, err := newMCPHub(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := h.Upsert(mcpCfg{Name: "one", Command: "echo", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "mcp.json"))
	blockPath(t, filepath.Join(dir, "mcp.json"))

	if _, err := h.Upsert(mcpCfg{ID: cfg.ID, Name: "one", Command: "changed", Enabled: true}); err == nil {
		t.Fatal("expected failed save")
	}
	if got := h.List()[0].Command; got != "echo" {
		t.Fatalf("command after failed edit = %q, want echo", got)
	}
	if _, err := h.Upsert(mcpCfg{Name: "two", Command: "echo"}); err == nil {
		t.Fatal("expected failed save")
	}
	if len(h.List()) != 1 {
		t.Fatal("a server that failed to save must not appear in the list")
	}
	if err := h.Delete(cfg.ID); err == nil {
		t.Fatal("expected failed save")
	}
	if len(h.List()) != 1 {
		t.Fatal("a delete that failed to save must not remove the server")
	}
}

func TestRagStoreRollsBackOnFailedSave(t *testing.T) {
	dir := t.TempDir()
	s, err := newRagStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	doc := ragDoc{ID: "aaaaaaaaaaaa", Name: "a.txt", Enabled: true}
	if err := s.Add(doc, []ragChunk{{Text: "hello", Vec: []float32{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, "index.json")
	os.Remove(idx)
	blockPath(t, idx)

	doc2 := ragDoc{ID: "bbbbbbbbbbbb", Name: "b.txt", Enabled: true}
	if err := s.Add(doc2, []ragChunk{{Text: "x", Vec: []float32{1, 0}}}); err == nil {
		t.Fatal("expected failed index save")
	}
	if len(s.List()) != 1 {
		t.Fatalf("index in memory has %d docs, want 1", len(s.List()))
	}
	if _, err := os.Stat(filepath.Join(dir, doc2.ID+".json")); !os.IsNotExist(err) {
		t.Fatal("the orphaned chunk file should be cleaned up")
	}
	// Replacing a doc by name must keep the old one if the index can't be saved.
	if err := s.Add(ragDoc{ID: "cccccccccccc", Name: "a.txt", Enabled: true}, []ragChunk{{Text: "new", Vec: []float32{0, 1}}}); err == nil {
		t.Fatal("expected failed index save")
	}
	if _, err := os.Stat(filepath.Join(dir, doc.ID+".json")); err != nil {
		t.Fatal("the existing document's file must survive a failed replace")
	}
	if err := s.Delete(doc.ID); err == nil || len(s.List()) != 1 {
		t.Fatal("a failed delete must leave the document in place")
	}
	if err := s.SetEnabled(doc.ID, false); err == nil || !s.List()[0].Enabled {
		t.Fatal("a failed SetEnabled must be rolled back")
	}
}

func TestRestoreReplaceRefusesBackupWithoutChats(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put("keep1", []byte(`{"id":"keep1","updated":1}`)); err != nil {
		t.Fatal(err)
	}
	a := &App{store: st}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("settings.json")
	f.Write([]byte(`{}`))
	zw.Close()
	rec := httptest.NewRecorder()
	a.restore(rec, httptest.NewRequest("POST", "/api/restore?mode=replace&settings=1", bytes.NewReader(buf.Bytes())))
	if rec.Code != 400 {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	if _, err := st.Get("keep1"); err != nil {
		t.Fatal("replace with an empty backup deleted existing chats")
	}
}

func TestImportKeepsNewerChats(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.Put("c1", []byte(`{"id":"c1","title":"new","updated":200}`))
	a := &App{store: st}
	body := `{"chats":[{"id":"c1","title":"old","updated":100},{"id":"c2","title":"fresh","updated":50}]}`
	rec := httptest.NewRecorder()
	a.importChats(rec, httptest.NewRequest("POST", "/api/import", strings.NewReader(body)))
	if !strings.Contains(rec.Body.String(), `"imported":1`) || !strings.Contains(rec.Body.String(), `"skipped":1`) {
		t.Fatalf("unexpected result %s", rec.Body)
	}
	if b, _ := st.Get("c1"); !strings.Contains(string(b), `"new"`) {
		t.Fatal("import overwrote a newer chat")
	}
}
