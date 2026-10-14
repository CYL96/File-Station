package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentSharesAndBoundaryChanges(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "storage")
	outside := filepath.Join(base, "outside")
	ro := filepath.Join(base, "read-only")
	for _, p := range []string{storage, outside, ro, filepath.Join(storage, "first"), filepath.Join(storage, "second")} {
		if err := os.MkdirAll(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(storage, "first", "keep.txt"), []byte("preserved"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(storage, "escape")); err != nil {
		t.Fatal(err)
	}
	c := Config{IP: "127.0.0.1", Port: 8080, Password: "123123", StateDir: filepath.Join(base, "state"), AllowedRoots: []AllowedRoot{{Path: storage}, {Path: ro, ReadOnly: true}}, Shares: []Share{{ID: "original", Name: "Original", Path: filepath.Join(storage, "first")}}}
	var app *Server
	var srv *httptest.Server
	var client *http.Client
	var csrf string
	request := func(path string, body any, want int) []byte {
		t.Helper()
		var data io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.NewReader(raw)
		}
		method := "GET"
		if body != nil {
			method = "POST"
		}
		req, err := http.NewRequest(method, srv.URL+path, data)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", srv.URL)
		req.Header.Set("X-CSRF-Token", csrf)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, response.StatusCode, want, raw)
		}
		return raw
	}
	start := func() {
		t.Helper()
		var err error
		app, err = New(c, base)
		if err != nil {
			t.Fatal(err)
		}
		srv = httptest.NewServer(app)
		jar, _ := cookiejar.New(nil)
		client = &http.Client{Jar: jar}
		var auth struct {
			CSRF string `json:"csrf"`
		}
		if err = json.Unmarshal(request("/api/login", map[string]string{"password": "123123"}, 200), &auth); err != nil {
			t.Fatal(err)
		}
		csrf = auth.CSRF
	}
	stop := func() { srv.Close(); app.Close() }
	start()
	// All settings routes use the existing administrator/CSRF guard.
	oldCSRF := csrf
	csrf = ""
	request("/api/settings/shares/remove", map[string]string{"id": "original"}, 403)
	csrf = oldCSRF
	save := func(id, name, path string, readOnly bool, want int) {
		t.Helper()
		request("/api/settings/shares/save", map[string]any{"id": id, "name": name, "path": path, "readOnly": readOnly}, want)
	}
	save("original", "Renamed", filepath.Join(storage, "first"), true, 200)
	request("/api/mkdir", map[string]string{"share": "original", "name": "blocked"}, 403)
	save("", "Escape", filepath.Join(storage, "escape"), false, 403)
	save("", "Outside", outside, false, 403)
	save("", "Overlap", storage, false, 409)
	save("", "Read only", ro, false, 403)
	request("/api/settings/directories?root="+storage+"&path=escape", nil, 403)
	request("/api/settings/directories?root="+storage+"&path=..", nil, 403)
	request("/api/settings/directories?root="+outside, nil, 403)
	save("", "Second", filepath.Join(storage, "second"), false, 200)
	stop()
	// Deployment may change bootstrap fields, but must never reconcile existing settings.
	c.Shares = []Share{{ID: "replacement", Name: "Replaced by deployment", Path: outside}}
	start()
	var saved struct {
		Shares []struct {
			ID, Name, Path      string
			ReadOnly, Available bool
		}
		AllowedRoots []AllowedRoot
	}
	if err := json.Unmarshal(request("/api/settings/shares", nil, 200), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Shares) != 2 || saved.Shares[0].ID != "original" || saved.Shares[0].Name != "Renamed" || !saved.Shares[0].ReadOnly {
		t.Fatalf("deployment overwrote settings: %+v", saved.Shares)
	}
	// Persisted metadata prevents path changes/removal that strand trash.
	if _, err := app.db.Exec("INSERT INTO trash(id,share,path,deleted_at,size,is_dir) VALUES('trash','original','keep.txt','now',9,0)"); err != nil {
		t.Fatal(err)
	}
	save("original", "Renamed", ro, true, 409)
	request("/api/settings/shares/remove", map[string]string{"id": "original"}, 409)
	if _, err := app.db.Exec("DELETE FROM trash WHERE id='trash'"); err != nil {
		t.Fatal(err)
	}
	// Queue a real copy behind the filesystem lock; edits must reject before it runs.
	save("original", "Renamed", filepath.Join(storage, "first"), false, 200)
	app.mutation.Lock()
	request("/api/operation", map[string]any{"kind": "copy", "share": "original", "paths": []string{"keep.txt"}, "destinationShare": saved.Shares[1].ID, "conflict": "error"}, 200)
	save("original", "Busy", filepath.Join(storage, "first"), true, 409)
	request("/api/settings/shares/remove", map[string]string{"id": saved.Shares[1].ID}, 409)
	app.CancelTasks()
	app.mutation.Unlock()
	app.workers.Wait()
	for _, sh := range saved.Shares {
		request("/api/settings/shares/remove", map[string]string{"id": sh.ID}, 200)
	}
	if raw, err := os.ReadFile(filepath.Join(storage, "first", "keep.txt")); err != nil || string(raw) != "preserved" {
		t.Fatalf("remove changed disk data: %s %v", raw, err)
	}
	stop()
	start()
	var shares []Share
	if err := json.Unmarshal(request("/api/shares", nil, 200), &shares); err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Fatalf("empty list reseeded: %+v", shares)
	}
	save("", "Persisted", filepath.Join(storage, "first"), false, 200)
	stop()
	c.AllowedRoots = []AllowedRoot{{Path: ro, ReadOnly: true}}
	start()
	defer stop()
	if err := json.Unmarshal(request("/api/settings/shares", nil, 200), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Shares) != 1 || saved.Shares[0].Available {
		t.Fatalf("boundary change did not preserve disabled share: %+v", saved)
	}
	request("/api/files?share="+saved.Shares[0].ID, nil, 403)
}
