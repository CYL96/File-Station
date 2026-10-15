package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPersistentSharesAndAvailabilityChanges(t *testing.T) {
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
	c := Config{IP: "127.0.0.1", Port: 8080, Password: "123123", StateDir: filepath.Join(base, "state"), Shares: []Share{{ID: "original", Name: "Original", Path: filepath.Join(storage, "first")}}}
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
	// Host symlinks are canonicalized, with no deployment whitelist.
	save("", "Outside", filepath.Join(storage, "escape"), false, 200)
	save("", "Duplicate canonical path", outside, false, 409)
	save("", "Overlap", storage, false, 409)
	request("/api/settings/directories?path="+url.QueryEscape(outside), nil, 200)
	request("/api/settings/directories?path="+url.QueryEscape(filepath.Join(storage, "escape")), nil, 200)
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
	}
	if err := json.Unmarshal(request("/api/settings/shares", nil, 200), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Shares) != 3 || saved.Shares[0].ID != "original" || saved.Shares[0].Name != "Renamed" || !saved.Shares[0].ReadOnly || saved.Shares[1].Path != outside {
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
	request("/api/operation", map[string]any{"kind": "copy", "share": "original", "paths": []string{"keep.txt"}, "destinationShare": saved.Shares[2].ID, "conflict": "error"}, 200)
	save("original", "Busy", filepath.Join(storage, "first"), true, 409)
	request("/api/settings/shares/remove", map[string]string{"id": saved.Shares[2].ID}, 409)
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
	if err := os.Rename(filepath.Join(storage, "first"), filepath.Join(storage, "unmounted")); err != nil {
		t.Fatal(err)
	}
	start()
	defer stop()
	if err := json.Unmarshal(request("/api/settings/shares", nil, 200), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Shares) != 1 || saved.Shares[0].Available {
		t.Fatalf("missing directory did not preserve disabled share: %+v", saved)
	}
	request("/api/files?share="+saved.Shares[0].ID, nil, 403)
}

func TestSharesStartEmptyAndStayEmptyAcrossBootstrapChanges(t *testing.T) {
	f := newFileSettingsFixture(t)
	assertFileViewDocument(t, f.request("GET", "/api/settings/shares", "", 200), `{"shares":[]}`)
	assertFileViewDocument(t, f.request("GET", "/api/shares", "", 200), `[]`)
	path := t.TempDir()
	f.stop()
	f.config.Shares = []Share{{ID: "ignored", Name: "Later bootstrap", Path: path}}
	f.start()
	assertFileViewDocument(t, f.request("GET", "/api/settings/shares", "", 200), `{"shares":[]}`)
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookies := f.client.Jar.Cookies(u)
	if len(cookies) != 1 || cookies[0].Name != "file_station_session" {
		t.Fatalf("session cookie was not renamed: %+v", cookies)
	}
}

func TestSharePrivatePathsAndHostSymlinks(t *testing.T) {
	base := t.TempDir()
	state := filepath.Join(base, "state")
	secretDir := filepath.Join(base, "credentials")
	public := filepath.Join(base, "public")
	for _, p := range []string{secretDir, public} {
		if err := os.Mkdir(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(secretDir, "password")
	if err := os.WriteFile(secret, []byte("secret-password"), 0600); err != nil {
		t.Fatal(err)
	}
	secretAlias := filepath.Join(base, "password-link")
	publicAlias := filepath.Join(base, "public-link")
	if err := os.Symlink(secret, secretAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(public, publicAlias); err != nil {
		t.Fatal(err)
	}
	app, err := New(Config{IP: "127.0.0.1", Port: 8080, StateDir: state, PasswordFile: secretAlias}, base)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	child := filepath.Join(state, "nested")
	if err = os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", base, state, child, secretDir} {
		t.Run(p, func(t *testing.T) {
			sh, err := app.openShare(Share{ID: "private", Name: "Private", Path: p})
			if sh.root != nil {
				sh.root.Close()
			}
			assertTextStatus(t, err, 403)
		})
	}
	sh, err := app.openShare(Share{ID: "public", Name: "Public", Path: publicAlias})
	if err != nil {
		t.Fatal(err)
	}
	defer sh.root.Close()
	if sh.Path != public {
		t.Fatalf("host symlink not canonicalized: %q", sh.Path)
	}
	if err = os.Symlink(secretDir, filepath.Join(public, "escape")); err != nil {
		t.Fatal(err)
	}
	assertTextStatus(t, noLinks(sh.root, "escape/password"), 403)
	assertTextStatus(t, app.settingsDirectories(httptest.NewRecorder(), httptest.NewRequest("GET", "/?path="+url.QueryEscape(child), nil)), 403)
	// Browsing a broad ancestor remains possible even when sharing it is unsafe.
	if err = app.settingsDirectories(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatalf("browsing / failed: %v", err)
	}
	// /etc is allowed whenever the actual process can read and search it.
	if _, err = accessibleDirectory("/etc"); err == nil {
		systemShare, openErr := app.openShare(Share{ID: "system", Name: "System", Path: "/etc"})
		if openErr != nil {
			t.Fatalf("arbitrary system path blacklist: %v", openErr)
		}
		systemShare.root.Close()
	}
}

func TestCredentialSourcePrivacyAfterRestart(t *testing.T) {
	clearConfigEnvironment(t)
	f := newFileSettingsFixture(t)
	base := filepath.Dir(f.config.StateDir)
	sourceDir := filepath.Join(base, "source")
	credentialDir := filepath.Join(base, "credentials")
	aliasDir := filepath.Join(base, "source-alias")
	dataDir := filepath.Join(base, "data")
	for _, p := range []string{sourceDir, credentialDir, aliasDir, dataDir} {
		if err := os.Mkdir(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(sourceDir, "password")
	credential := filepath.Join(credentialDir, "password")
	for _, p := range []string{source, credential} {
		if err := os.WriteFile(p, []byte("123123\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(aliasDir, "password")
	if err := os.Symlink(source, alias); err != nil {
		t.Fatal(err)
	}
	save := func(p string, want int) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"name": "Directory", "path": p})
		if err != nil {
			t.Fatal(err)
		}
		f.request("POST", "/api/settings/shares/save", string(body), want)
	}
	// Previously saved shares must be disabled when source metadata is added.
	save(sourceDir, 200)
	f.stop()
	config := f.config
	config.Password = ""
	config.PasswordFile = credential
	config.PasswordSourceFile = alias
	config.Umask = "0022"
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	f.config, err = LoadConfig(writeLoaderConfig(t, string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	f.start()
	var settings struct {
		Shares []struct {
			ID        string
			Available bool
		}
	}
	if err := json.Unmarshal(f.request("GET", "/api/settings/shares", "", 200), &settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.Shares) != 1 || settings.Shares[0].Available {
		t.Fatalf("persisted source share privacy incorrect: %+v", settings)
	}
	f.request("GET", "/api/files?share="+settings.Shares[0].ID, "", 403)
	save(sourceDir, 403)
	save(aliasDir, 403)
	save(credentialDir, 403)
	save(dataDir, 200)
}

func TestDirectoryBrowserUsesAbsolutePathsAndFilters(t *testing.T) {
	f := newFileSettingsFixture(t)
	base := filepath.Dir(f.config.StateDir)
	visible := filepath.Join(base, "container\\mount ")
	hidden := filepath.Join(base, ".webui-private")
	for _, p := range []string{visible, hidden} {
		if err := os.Mkdir(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "regular-file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(visible, filepath.Join(base, "directory-link")); err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Path    string
		Entries []struct{ Name, Path string }
	}
	raw := f.request("GET", "/api/settings/directories?path="+url.QueryEscape(base), "", 200)
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, entry := range listing.Entries {
		got[entry.Name] = entry.Path
	}
	if listing.Path != base || !reflect.DeepEqual(got, map[string]string{"container\\mount ": visible}) {
		t.Fatalf("directory filtering failed: %s", raw)
	}
	f.request("GET", "/api/settings/directories?path="+url.QueryEscape(visible), "", 200)
	body, err := json.Marshal(map[string]string{"name": "Host path", "path": visible})
	if err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/settings/shares/save", string(body), 200)
	if len(f.app.shares) != 1 || f.app.shares[0].Path != visible {
		t.Fatalf("saving changed host path bytes: %+v", f.app.shares)
	}
	f.request("GET", "/api/settings/directories?path=relative", "", 400)
	f.request("GET", "/api/settings/directories?path="+url.QueryEscape(hidden), "", 403)
	f.request("GET", "/api/settings/directories?path="+url.QueryEscape(f.config.StateDir), "", 403)
}

func TestShareAndDirectoryBrowserRequireEffectiveReadAndSearch(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root has DAC override; run as an unprivileged user to check permission denials")
	}
	f := newFileSettingsFixture(t)
	base := t.TempDir()
	for _, mode := range []os.FileMode{0000, 0400, 0100} {
		t.Run(mode.String(), func(t *testing.T) {
			p := filepath.Join(base, mode.String())
			if err := os.Mkdir(p, mode); err != nil {
				t.Fatal(err)
			}
			f.t.Cleanup(func() { _ = os.Chmod(p, 0700) })
			sh, err := f.app.openShare(Share{ID: "denied", Name: "Denied", Path: p})
			if sh.root != nil {
				sh.root.Close()
			}
			assertTextStatus(t, err, 403)
			f.request("GET", "/api/settings/directories?path="+url.QueryEscape(p), "", 403)
		})
	}
	readable := filepath.Join(base, "readable-not-writable")
	if err := os.Mkdir(readable, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readable, 0700) })
	sh, err := f.app.openShare(Share{ID: "readable", Name: "Readable", Path: readable})
	if err != nil {
		t.Fatalf("read/search must be enough to add a writable share: %v", err)
	}
	sh.root.Close()
	f.request("GET", "/api/settings/directories?path="+url.QueryEscape(readable), "", 200)
	body, err := json.Marshal(map[string]any{"name": "Readable", "path": readable, "readOnly": false})
	if err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/settings/shares/save", string(body), 200)
	body, err = json.Marshal(map[string]string{"share": f.app.shares[0].ID, "name": "write-denied"})
	if err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/mkdir", string(body), 403)
	raw := f.request("GET", "/api/settings/directories?path="+url.QueryEscape(base), "", 200)
	var listing struct{ Entries []struct{ Name string } }
	if err = json.Unmarshal(raw, &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "readable-not-writable" {
		t.Fatalf("unreadable or unsearchable directory leaked into browser: %s", raw)
	}
}

func TestWithinFilesystemRoot(t *testing.T) {
	for _, p := range []string{"/", "/etc", "/mnt/storage/nested"} {
		if !within("/", p) || !overlaps("/", p) {
			t.Fatalf("filesystem root does not contain %q", p)
		}
	}
	if within("/mnt/data", "/mnt/database") {
		t.Fatal("sibling prefix incorrectly treated as descendant")
	}
}
