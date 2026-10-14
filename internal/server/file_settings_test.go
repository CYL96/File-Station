package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const defaultFileViewDocument = `{"showHidden":false,"columns":[{"key":"name","visible":true},{"key":"size","visible":true},{"key":"modified","visible":true},{"key":"type","visible":true},{"key":"extension","visible":false},{"key":"permissions","visible":false},{"key":"owner","visible":false},{"key":"group","visible":false},{"key":"created","visible":false}],"sort":{"key":"name","ascending":true}}`
const customFileViewDocument = `{"showHidden":true,"columns":[{"key":"created","visible":true},{"key":"type","visible":false},{"key":"owner","visible":true},{"key":"name","visible":true},{"key":"extension","visible":true},{"key":"modified","visible":true},{"key":"group","visible":true},{"key":"size","visible":false},{"key":"permissions","visible":true}],"sort":{"key":"created","ascending":false}}`
const legacyFileViewDocument = `{"showHidden":true,"columns":[{"key":"type","visible":false},{"key":"name","visible":true},{"key":"modified","visible":true},{"key":"size","visible":false}],"sort":{"key":"size","ascending":false}}`
const migratedFileViewDocument = `{"showHidden":true,"columns":[{"key":"type","visible":false},{"key":"name","visible":true},{"key":"modified","visible":true},{"key":"size","visible":false},{"key":"extension","visible":false},{"key":"permissions","visible":false},{"key":"owner","visible":false},{"key":"group","visible":false},{"key":"created","visible":false}],"sort":{"key":"size","ascending":false}}`

type fileSettingsFixture struct {
	t      *testing.T
	config Config
	static string
	app    *Server
	srv    *httptest.Server
	client *http.Client
	csrf   string
}

func newFileSettingsFixture(t *testing.T) *fileSettingsFixture {
	t.Helper()
	base := t.TempDir()
	fixture := &fileSettingsFixture{
		t:      t,
		static: base,
		config: Config{IP: "127.0.0.1", Port: 8080, Password: "123123", StateDir: filepath.Join(base, "state"), AllowedRoots: []AllowedRoot{{Path: t.TempDir()}}},
	}
	t.Cleanup(fixture.stop)
	fixture.start()
	return fixture
}

func (f *fileSettingsFixture) start() {
	f.t.Helper()
	var err error
	f.app, err = New(f.config, f.static)
	if err != nil {
		f.t.Fatal(err)
	}
	f.srv = httptest.NewServer(f.app)
	jar, err := cookiejar.New(nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.client = &http.Client{Jar: jar}
	var auth struct {
		CSRF string `json:"csrf"`
	}
	if err = json.Unmarshal(f.request("POST", "/api/login", `{"password":"123123"}`, http.StatusOK), &auth); err != nil {
		f.t.Fatal(err)
	}
	f.csrf = auth.CSRF
}

func (f *fileSettingsFixture) stop() {
	if f.srv != nil {
		f.srv.Close()
		f.srv = nil
	}
	if f.app != nil {
		f.app.Close()
		f.app = nil
	}
}

func (f *fileSettingsFixture) request(method, path, body string, want int) []byte {
	f.t.Helper()
	return f.requestWithAuth(method, path, body, f.client, f.csrf, f.srv.URL, want)
}

func (f *fileSettingsFixture) requestWithAuth(method, path, body string, client *http.Client, csrf, origin string, want int) []byte {
	f.t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, bytes.NewBufferString(body))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("X-CSRF-Token", csrf)
	response, err := client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	if response.StatusCode != want {
		f.t.Fatalf("%s %s = %d, want %d: %s", method, path, response.StatusCode, want, raw)
	}
	if response.Header.Get("Content-Type") != "application/json" || response.Header.Get("Cache-Control") != "no-store" {
		f.t.Fatalf("settings response lacks JSON/no-store headers: %v", response.Header)
	}
	return raw
}

func assertFileViewDocument(t *testing.T, raw []byte, want string) {
	t.Helper()
	var actual, expected any
	if err := json.Unmarshal(raw, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("file settings = %s, want %s", raw, want)
	}
}

func TestFileSettingsDefaultsAndPersistence(t *testing.T) {
	f := newFileSettingsFixture(t)
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), defaultFileViewDocument)
	var count int
	if err := f.app.db.QueryRow("SELECT count(*) FROM settings_meta WHERE key='file_view'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("reading defaults unexpectedly persisted a document")
	}
	// False visibility/sort values and a moved, visible name column survive a restart.
	assertFileViewDocument(t, f.request("POST", "/api/settings/files/save", customFileViewDocument, http.StatusOK), customFileViewDocument)
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), customFileViewDocument)
	var stored string
	if err := f.app.db.QueryRow("SELECT value FROM settings_meta WHERE key='file_view'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	assertFileViewDocument(t, []byte(stored), customFileViewDocument)
	f.stop()
	f.start()
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), customFileViewDocument)
	// Saving an explicit false resets showHidden rather than treating it as omitted.
	assertFileViewDocument(t, f.request("POST", "/api/settings/files/save", defaultFileViewDocument, http.StatusOK), defaultFileViewDocument)
	f.stop()
	f.start()
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), defaultFileViewDocument)
	for _, key := range []string{"name", "size", "modified", "type", "extension", "permissions", "owner", "group", "created"} {
		var document fileViewSettings
		if err := json.Unmarshal([]byte(customFileViewDocument), &document); err != nil {
			t.Fatal(err)
		}
		document.Sort.Key = key
		document.Sort.Ascending = true
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		assertFileViewDocument(t, f.request("POST", "/api/settings/files/save", string(raw), http.StatusOK), string(raw))
	}
}

func TestFileSettingsMigratesLegacyDocument(t *testing.T) {
	f := newFileSettingsFixture(t)
	if _, err := f.app.db.Exec("INSERT INTO settings_meta(key,value) VALUES('file_view',?)", legacyFileViewDocument); err != nil {
		t.Fatal(err)
	}
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), migratedFileViewDocument)
	var stored string
	if err := f.app.db.QueryRow("SELECT value FROM settings_meta WHERE key='file_view'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	assertFileViewDocument(t, []byte(stored), migratedFileViewDocument)
	f.stop()
	f.start()
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), migratedFileViewDocument)
	if err := f.app.db.QueryRow("SELECT value FROM settings_meta WHERE key='file_view'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	assertFileViewDocument(t, []byte(stored), migratedFileViewDocument)
}

func TestFileSettingsMigrationRereadsConcurrentSave(t *testing.T) {
	f := newFileSettingsFixture(t)
	if _, err := f.app.db.Exec("INSERT INTO settings_meta(key,value) VALUES('file_view',?)", legacyFileViewDocument); err != nil {
		t.Fatal(err)
	}
	// Simulate a save winning while migration updates its previously read document.
	// Ignoring the outer update reports zero affected rows and forces a fresh read.
	if _, err := f.app.db.Exec(`CREATE TRIGGER concurrent_file_view_save BEFORE UPDATE ON settings_meta
		WHEN OLD.key='file_view' AND OLD.value='` + legacyFileViewDocument + `' AND NEW.value!='` + customFileViewDocument + `'
		BEGIN
			UPDATE settings_meta SET value='` + customFileViewDocument + `' WHERE key='file_view';
			SELECT RAISE(IGNORE);
		END`); err != nil {
		t.Fatal(err)
	}
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), customFileViewDocument)
	var stored string
	if err := f.app.db.QueryRow("SELECT value FROM settings_meta WHERE key='file_view'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	assertFileViewDocument(t, []byte(stored), customFileViewDocument)
}

func TestFileSettingsMigrationStorageFailurePreservesLegacy(t *testing.T) {
	f := newFileSettingsFixture(t)
	if _, err := f.app.db.Exec("INSERT INTO settings_meta(key,value) VALUES('file_view',?)", legacyFileViewDocument); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.db.Exec(`CREATE TRIGGER reject_file_view BEFORE UPDATE ON settings_meta WHEN OLD.key='file_view' BEGIN SELECT RAISE(FAIL,'blocked write'); END`); err != nil {
		t.Fatal(err)
	}
	f.request("GET", "/api/settings/files", "", http.StatusInternalServerError)
	var stored string
	if err := f.app.db.QueryRow("SELECT value FROM settings_meta WHERE key='file_view'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != legacyFileViewDocument {
		t.Fatal("failed migration changed the legacy document")
	}
}

func TestFileSettingsInvalidWritesPreserveDocument(t *testing.T) {
	f := newFileSettingsFixture(t)
	f.request("POST", "/api/settings/files/save", customFileViewDocument, http.StatusOK)
	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"malformed JSON", `{"showHidden":`},
		{"null document", `null`},
		{"array document", `[]`},
		{"empty document", `{}`},
		{"missing showHidden", strings.Replace(defaultFileViewDocument, `"showHidden":false,`, "", 1)},
		{"null showHidden", strings.Replace(defaultFileViewDocument, `"showHidden":false`, `"showHidden":null`, 1)},
		{"wrong showHidden type", strings.Replace(defaultFileViewDocument, `"showHidden":false`, `"showHidden":"true"`, 1)},
		{"missing columns", `{"showHidden":false,"sort":{"key":"name","ascending":true}}`},
		{"null columns", `{"showHidden":false,"columns":null,"sort":{"key":"name","ascending":true}}`},
		{"empty columns", `{"showHidden":false,"columns":[],"sort":{"key":"name","ascending":true}}`},
		{"missing column", strings.Replace(defaultFileViewDocument, `,{"key":"created","visible":false}`, "", 1)},
		{"extra column", strings.Replace(defaultFileViewDocument, `],"sort"`, `,{"key":"type","visible":false}],"sort"`, 1)},
		{"duplicate column", strings.Replace(defaultFileViewDocument, `"key":"modified"`, `"key":"size"`, 1)},
		{"unknown column", strings.Replace(defaultFileViewDocument, `"key":"owner"`, `"key":"unknown"`, 1)},
		{"missing column key", strings.Replace(defaultFileViewDocument, `"key":"group",`, "", 1)},
		{"missing visibility", strings.Replace(defaultFileViewDocument, `"key":"size","visible":true`, `"key":"size"`, 1)},
		{"null visibility", strings.Replace(defaultFileViewDocument, `"key":"permissions","visible":false`, `"key":"permissions","visible":null`, 1)},
		{"wrong visibility type", strings.Replace(defaultFileViewDocument, `"key":"extension","visible":false`, `"key":"extension","visible":0`, 1)},
		{"hidden name", strings.Replace(defaultFileViewDocument, `"key":"name","visible":true`, `"key":"name","visible":false`, 1)},
		{"missing sort", strings.Replace(defaultFileViewDocument, `,"sort":{"key":"name","ascending":true}`, "", 1)},
		{"null sort", strings.Replace(defaultFileViewDocument, `"sort":{"key":"name","ascending":true}`, `"sort":null`, 1)},
		{"unknown sort key", strings.Replace(defaultFileViewDocument, `"sort":{"key":"name"`, `"sort":{"key":"unknown"`, 1)},
		{"missing sort key", strings.Replace(defaultFileViewDocument, `"sort":{"key":"name",`, `"sort":{`, 1)},
		{"missing sort direction", strings.Replace(defaultFileViewDocument, `,"ascending":true`, "", 1)},
		{"null sort direction", strings.Replace(defaultFileViewDocument, `"ascending":true`, `"ascending":null`, 1)},
		{"wrong sort direction type", strings.Replace(defaultFileViewDocument, `"ascending":true`, `"ascending":"true"`, 1)},
		{"unknown root field", customFileViewDocument[:len(customFileViewDocument)-1] + `,"extra":true}`},
		{"unknown column field", strings.Replace(defaultFileViewDocument, `"key":"created","visible":false`, `"key":"created","visible":false,"extra":true`, 1)},
		{"unknown sort field", strings.Replace(defaultFileViewDocument, `"ascending":true`, `"ascending":true,"extra":true`, 1)},
		{"trailing document", customFileViewDocument + `{}`},
		{"valid legacy document", legacyFileViewDocument},
		{"malformed legacy document", strings.Replace(legacyFileViewDocument, `"key":"size","visible":false`, `"key":"size"`, 1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f.request("POST", "/api/settings/files/save", test.body, http.StatusBadRequest)
			assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), customFileViewDocument)
		})
	}
	f.stop()
	f.start()
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), customFileViewDocument)
}

func TestFileSettingsAuthAndCSRF(t *testing.T) {
	f := newFileSettingsFixture(t)
	f.requestWithAuth("GET", "/api/settings/files", "", http.DefaultClient, "", f.srv.URL, http.StatusUnauthorized)
	f.requestWithAuth("POST", "/api/settings/files/save", customFileViewDocument, http.DefaultClient, f.csrf, f.srv.URL, http.StatusUnauthorized)
	f.requestWithAuth("POST", "/api/settings/files/save", customFileViewDocument, f.client, "", f.srv.URL, http.StatusForbidden)
	f.requestWithAuth("POST", "/api/settings/files/save", customFileViewDocument, f.client, "incorrect", f.srv.URL, http.StatusForbidden)
	f.requestWithAuth("POST", "/api/settings/files/save", customFileViewDocument, f.client, f.csrf, "https://foreign.example", http.StatusForbidden)
	assertFileViewDocument(t, f.requestWithAuth("GET", "/api/settings/files", "", f.client, "", f.srv.URL, http.StatusOK), defaultFileViewDocument)
	f.request("POST", "/api/settings/files/save", customFileViewDocument, http.StatusOK)
	f.requestWithAuth("POST", "/api/settings/files/save", defaultFileViewDocument, f.client, "", f.srv.URL, http.StatusForbidden)
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), customFileViewDocument)
}

func TestFileSettingsStorageFailuresDoNotReturnDefaultsOrSuccess(t *testing.T) {
	f := newFileSettingsFixture(t)
	f.request("POST", "/api/settings/files/save", customFileViewDocument, http.StatusOK)
	// A database rejection must not send the requested document as a successful save.
	if _, err := f.app.db.Exec(`CREATE TRIGGER reject_file_view BEFORE UPDATE ON settings_meta WHEN OLD.key='file_view' BEGIN SELECT RAISE(FAIL,'blocked write'); END`); err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/settings/files/save", defaultFileViewDocument, http.StatusInternalServerError)
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), customFileViewDocument)
	if _, err := f.app.db.Exec("DROP TRIGGER reject_file_view"); err != nil {
		t.Fatal(err)
	}
	for _, document := range []string{
		`not JSON`, `null`, `{}`, customFileViewDocument + `{}`, customFileViewDocument[:len(customFileViewDocument)-1] + `,"extra":true}`,
		strings.Replace(defaultFileViewDocument, `,{"key":"created","visible":false}`, "", 1),
		strings.Replace(legacyFileViewDocument, `"showHidden":true,`, "", 1),
		strings.Replace(legacyFileViewDocument, `"showHidden":true`, `"showHidden":null`, 1),
		strings.Replace(legacyFileViewDocument, `"key":"size"`, `"key":"type"`, 1),
		strings.Replace(legacyFileViewDocument, `"key":"size","visible":false`, `"key":"owner","visible":false`, 1),
		strings.Replace(legacyFileViewDocument, `"key":"size","visible":false`, `"key":"size"`, 1),
		strings.Replace(legacyFileViewDocument, `"key":"type","visible":false`, `"key":"type","visible":null`, 1),
		strings.Replace(legacyFileViewDocument, `"key":"name","visible":true`, `"key":"name","visible":false`, 1),
		strings.Replace(legacyFileViewDocument, `"sort":{"key":"size"`, `"sort":{"key":"owner"`, 1),
		strings.Replace(legacyFileViewDocument, `,"ascending":false`, "", 1),
		strings.Replace(legacyFileViewDocument, `"ascending":false`, `"ascending":null`, 1),
		legacyFileViewDocument + `{}`,
		legacyFileViewDocument[:len(legacyFileViewDocument)-1] + `,"extra":true}`,
	} {
		if _, err := f.app.db.Exec("UPDATE settings_meta SET value=? WHERE key='file_view'", document); err != nil {
			t.Fatal(err)
		}
		f.request("GET", "/api/settings/files", "", http.StatusInternalServerError)
		var stored string
		if err := f.app.db.QueryRow("SELECT value FROM settings_meta WHERE key='file_view'").Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != document {
			t.Fatal("reading corrupted settings silently replaced them")
		}
	}
	// The sessions table remains available, so failure is in settings retrieval, not auth.
	if _, err := f.app.db.Exec("ALTER TABLE settings_meta RENAME TO unreadable_settings_meta"); err != nil {
		t.Fatal(err)
	}
	f.request("GET", "/api/settings/files", "", http.StatusInternalServerError)
	f.request("POST", "/api/settings/files/save", customFileViewDocument, http.StatusInternalServerError)
}
