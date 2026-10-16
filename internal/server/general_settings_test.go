package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

const defaultGeneralDocument = `{"dateFormat":"locale","permissionFormat":"octal"}`
const customGeneralDocument = `{"dateFormat":"dd/MM/yyyy","permissionFormat":"symbolic"}`

func assertGeneralSettingsDocument(t *testing.T, raw []byte, want string) {
	t.Helper()
	var gotSettings, wantSettings generalSettings
	if err := json.Unmarshal(raw, &gotSettings); err != nil {
		t.Fatalf("decode general settings %s: %v", raw, err)
	}
	if err := json.Unmarshal([]byte(want), &wantSettings); err != nil {
		t.Fatalf("decode expected general settings %s: %v", want, err)
	}
	if gotSettings != wantSettings {
		t.Fatalf("general settings = %+v, want %+v", gotSettings, wantSettings)
	}
}

func TestGeneralSettingsDefaultsDoNotPersist(t *testing.T) {
	f := newFileSettingsFixture(t)
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), defaultGeneralDocument)
	var count int
	if err := f.app.db.QueryRow("SELECT count(*) FROM settings_meta WHERE key='general'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("reading defaults unexpectedly persisted a document")
	}
	f.stop()
	f.start()
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), defaultGeneralDocument)
}

func TestGeneralSettingsFormatsRoundTripAndPersist(t *testing.T) {
	f := newFileSettingsFixture(t)
	for _, dateFormat := range []string{"yyyy-MM-dd", "yyyy/MM/dd", "dd/MM/yyyy", "MM/dd/yyyy", "locale"} {
		for _, permissionFormat := range []string{"octal", "symbolic"} {
			t.Run(dateFormat+"/"+permissionFormat, func(t *testing.T) {
				document := `{"dateFormat":"` + dateFormat + `","permissionFormat":"` + permissionFormat + `"}`
				assertGeneralSettingsDocument(t, f.request("POST", "/api/settings/general/save", document, http.StatusOK), document)
				assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), document)
				var stored string
				if err := f.app.db.QueryRow("SELECT value FROM settings_meta WHERE key='general'").Scan(&stored); err != nil {
					t.Fatal(err)
				}
				assertGeneralSettingsDocument(t, []byte(stored), document)
				f.stop()
				f.start()
				assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), document)
			})
		}
	}
	// General settings must not change the separately persisted file-view settings.
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), defaultFileViewDocument)
}

func TestGeneralSettingsLegacyStorageDefaultsPermissionFormat(t *testing.T) {
	f := newFileSettingsFixture(t)
	const legacyDocument = `{"dateFormat":"yyyy/MM/dd"}`
	const want = `{"dateFormat":"yyyy/MM/dd","permissionFormat":"octal"}`
	if _, err := f.app.db.Exec("INSERT INTO settings_meta(key,value) VALUES('general',?)", legacyDocument); err != nil {
		t.Fatal(err)
	}
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), want)
	f.stop()
	f.start()
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), want)
	var stored string
	if err := f.app.db.QueryRow("SELECT value FROM settings_meta WHERE key='general'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != legacyDocument {
		t.Fatal("reading legacy settings unexpectedly rewrote the document")
	}
}

func TestGeneralSettingsLegacyRequestDefaultsPermissionFormat(t *testing.T) {
	f := newFileSettingsFixture(t)
	f.request("POST", "/api/settings/general/save", customGeneralDocument, http.StatusOK)
	const legacyDocument = `{"dateFormat":"MM/dd/yyyy"}`
	const want = `{"dateFormat":"MM/dd/yyyy","permissionFormat":"octal"}`
	assertGeneralSettingsDocument(t, f.request("POST", "/api/settings/general/save", legacyDocument, http.StatusOK), want)
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), want)
	f.stop()
	f.start()
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), want)
}

func TestGeneralSettingsInvalidWritesPreserveDocument(t *testing.T) {
	f := newFileSettingsFixture(t)
	f.request("POST", "/api/settings/general/save", customGeneralDocument, http.StatusOK)
	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"malformed JSON", `{"dateFormat":`},
		{"null document", `null`},
		{"array document", `[]`},
		{"string document", `"locale"`},
		{"missing format", `{}`},
		{"null format", `{"dateFormat":null}`},
		{"wrong format type", `{"dateFormat":24}`},
		{"empty format", `{"dateFormat":""}`},
		{"unknown format", `{"dateFormat":"iso"}`},
		{"wrong case", `{"dateFormat":"YYYY-MM-DD"}`},
		{"padded format", `{"dateFormat":" locale "}`},
		{"missing date with permission", `{"permissionFormat":"symbolic"}`},
		{"null date with permission", `{"dateFormat":null,"permissionFormat":"symbolic"}`},
		{"null permission", `{"dateFormat":"locale","permissionFormat":null}`},
		{"wrong permission type", `{"dateFormat":"locale","permissionFormat":24}`},
		{"boolean permission", `{"dateFormat":"locale","permissionFormat":false}`},
		{"object permission", `{"dateFormat":"locale","permissionFormat":{}}`},
		{"array permission", `{"dateFormat":"locale","permissionFormat":[]}`},
		{"empty permission", `{"dateFormat":"locale","permissionFormat":""}`},
		{"unknown permission", `{"dateFormat":"locale","permissionFormat":"rwx"}`},
		{"wrong permission case", `{"dateFormat":"locale","permissionFormat":"OCTAL"}`},
		{"padded permission", `{"dateFormat":"locale","permissionFormat":" symbolic "}`},
		{"unknown field", `{"dateFormat":"locale","extra":true}`},
		{"trailing document", defaultGeneralDocument + `{}`},
		{"trailing null", defaultGeneralDocument + `null`},
		{"trailing junk", defaultGeneralDocument + `invalid`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f.request("POST", "/api/settings/general/save", test.body, http.StatusBadRequest)
			assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), customGeneralDocument)
			var stored string
			if err := f.app.db.QueryRow("SELECT value FROM settings_meta WHERE key='general'").Scan(&stored); err != nil {
				t.Fatal(err)
			}
			assertGeneralSettingsDocument(t, []byte(stored), customGeneralDocument)
		})
	}
	f.request("POST", "/api/settings/general/save", `{"dateFormat":null}`, http.StatusBadRequest)
	f.stop()
	f.start()
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), customGeneralDocument)
}

func TestGeneralSettingsAuthAndCSRF(t *testing.T) {
	f := newFileSettingsFixture(t)
	f.requestWithAuth("GET", "/api/settings/general", "", http.DefaultClient, "", f.srv.URL, http.StatusUnauthorized)
	f.requestWithAuth("POST", "/api/settings/general/save", customGeneralDocument, http.DefaultClient, f.csrf, f.srv.URL, http.StatusUnauthorized)
	assertGeneralSettingsDocument(t, f.requestWithAuth("GET", "/api/settings/general", "", f.client, "", f.srv.URL, http.StatusOK), defaultGeneralDocument)
	f.request("POST", "/api/settings/general/save", customGeneralDocument, http.StatusOK)
	for _, test := range []struct {
		name   string
		csrf   string
		origin string
	}{
		{"missing token", "", f.srv.URL},
		{"incorrect token", "incorrect", f.srv.URL},
		{"foreign origin", f.csrf, "https://foreign.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.requestWithAuth("POST", "/api/settings/general/save", defaultGeneralDocument, f.client, test.csrf, test.origin, http.StatusForbidden)
			assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), customGeneralDocument)
		})
	}
	if _, err := f.app.db.Exec("UPDATE sessions SET expires=0"); err != nil {
		t.Fatal(err)
	}
	f.request("GET", "/api/settings/general", "", http.StatusUnauthorized)
	f.request("POST", "/api/settings/general/save", defaultGeneralDocument, http.StatusUnauthorized)
	f.stop()
	f.start()
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), customGeneralDocument)
}

func TestGeneralSettingsCorruptStorageIsNotReplaced(t *testing.T) {
	f := newFileSettingsFixture(t)
	f.request("POST", "/api/settings/general/save", customGeneralDocument, http.StatusOK)
	for _, document := range []string{
		`not JSON`, `null`, `[]`, `{}`, `{"dateFormat":null}`, `{"dateFormat":false}`,
		`{"dateFormat":""}`, `{"dateFormat":"unknown"}`,
		`{"permissionFormat":"symbolic"}`, `{"dateFormat":null,"permissionFormat":"symbolic"}`,
		`{"dateFormat":"locale","permissionFormat":null}`, `{"dateFormat":"locale","permissionFormat":false}`,
		`{"dateFormat":"locale","permissionFormat":""}`, `{"dateFormat":"locale","permissionFormat":"unknown"}`,
		`{"dateFormat":"locale","permissionFormat":"OCTAL"}`, `{"dateFormat":"locale","permissionFormat":" symbolic "}`,
		`{"dateFormat":"locale","extra":true}`, defaultGeneralDocument + `{}`,
		defaultGeneralDocument + `null`, defaultGeneralDocument + `invalid`,
	} {
		t.Run(document, func(t *testing.T) {
			if _, err := f.app.db.Exec("UPDATE settings_meta SET value=? WHERE key='general'", document); err != nil {
				t.Fatal(err)
			}
			f.request("GET", "/api/settings/general", "", http.StatusInternalServerError)
			var stored string
			if err := f.app.db.QueryRow("SELECT value FROM settings_meta WHERE key='general'").Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != document {
				t.Fatal("reading corrupt settings silently replaced them")
			}
		})
	}
	// A valid explicit save can repair a corrupt document.
	assertGeneralSettingsDocument(t, f.request("POST", "/api/settings/general/save", customGeneralDocument, http.StatusOK), customGeneralDocument)
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), customGeneralDocument)
}

func TestGeneralSettingsStorageFailuresDoNotReturnDefaultsOrSuccess(t *testing.T) {
	f := newFileSettingsFixture(t)
	if _, err := f.app.db.Exec(`CREATE TRIGGER reject_general_insert BEFORE INSERT ON settings_meta WHEN NEW.key='general' BEGIN SELECT RAISE(FAIL,'blocked write'); END`); err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/settings/general/save", customGeneralDocument, http.StatusInternalServerError)
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), defaultGeneralDocument)
	if _, err := f.app.db.Exec("DROP TRIGGER reject_general_insert"); err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/settings/general/save", customGeneralDocument, http.StatusOK)
	if _, err := f.app.db.Exec(`CREATE TRIGGER reject_general_update BEFORE UPDATE ON settings_meta WHEN OLD.key='general' BEGIN SELECT RAISE(FAIL,'blocked write'); END`); err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/settings/general/save", defaultGeneralDocument, http.StatusInternalServerError)
	assertGeneralSettingsDocument(t, f.request("GET", "/api/settings/general", "", http.StatusOK), customGeneralDocument)
	// Keep sessions available so the following errors originate in settings storage.
	if _, err := f.app.db.Exec("ALTER TABLE settings_meta RENAME TO unreadable_settings_meta"); err != nil {
		t.Fatal(err)
	}
	f.request("GET", "/api/settings/general", "", http.StatusInternalServerError)
	f.request("POST", "/api/settings/general/save", defaultGeneralDocument, http.StatusInternalServerError)
}
