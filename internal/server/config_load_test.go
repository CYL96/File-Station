package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"PASSWORD", "UMASK", "ConfigPath"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func writeLoaderConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigFileIgnoresEnvironment(t *testing.T) {
	for _, mask := range []string{"0077", "", "invalid"} {
		t.Run("UMASK="+mask, func(t *testing.T) {
			for _, credential := range []struct {
				name         string
				json         string
				password     string
				passwordFile string
			}{
				{"password", `"password":"file-password"`, "file-password", ""},
				{"password file", `"passwordFile":"/file-secret"`, "", "/file-secret"},
			} {
				t.Run(credential.name, func(t *testing.T) {
					clearConfigEnvironment(t)
					t.Setenv("PASSWORD", "env-password")
					t.Setenv("UMASK", mask)
					t.Setenv("ConfigPath", "relative/private-password-marker")
					path := writeLoaderConfig(t, `{"ip":"10.0.0.1","port":9000,"stateDir":"/file-state",`+credential.json+`,"passwordSourceFile":"/file-source","umask":"0027","secureCookies":true,"shares":[{"id":"file","name":"File","path":"/file","readOnly":false}]}`)
					config, err := LoadConfig(path)
					if err != nil {
						t.Fatal(err)
					}
					if config.IP != "10.0.0.1" || config.Port != 9000 || config.StateDir != "/file-state" || config.Umask != "0027" || !config.SecureCookies {
						t.Fatal("environment changed file settings")
					}
					if config.Password != credential.password || config.PasswordFile != credential.passwordFile || config.PasswordSourceFile != "/file-source" {
						t.Fatal("environment changed file credentials")
					}
					if len(config.Shares) != 1 || config.Shares[0].ID != "file" || config.Shares[0].Name != "File" || config.Shares[0].Path != "/file" || config.Shares[0].ReadOnly {
						t.Fatalf("environment changed file shares: %+v", config.Shares)
					}
				})
			}
		})
	}
}

func TestLoadConfigFileOmittedFieldsIgnoreEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	path := writeLoaderConfig(t, `{"password":"file-password"}`)
	want, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PASSWORD", "env-password")
	t.Setenv("UMASK", "invalid")
	t.Setenv("ConfigPath", "")
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("environment must not populate omitted file fields")
	}
}

func TestLoadConfigMissingFileDoesNotFallBackToEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("PASSWORD", "env-password")
	t.Setenv("UMASK", "0022")
	path := filepath.Join(t.TempDir(), "missing.json")
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "open configuration") || !strings.Contains(err.Error(), path) {
		t.Fatalf("missing configuration must fail without falling back to environment: %v", err)
	}
}

func TestLoadConfigEnvironmentCredentials(t *testing.T) {
	for _, test := range []struct{ name, password string }{
		{"missing", ""},
		{"too short", "short"},
		{"verbatim including newline", "env-password\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnvironment(t)
			password := test.password
			t.Setenv("PASSWORD", password)
			config, err := LoadConfig("")
			if err != nil {
				t.Fatal(err)
			}
			if config.Password != password || config.PasswordFile != "" || config.PasswordSourceFile != "" {
				t.Fatal("PASSWORD must be used verbatim without secret-file fallbacks")
			}
			if config.StateDir != "/var/lib/file-station" {
				t.Fatal("unset ConfigPath must retain the default state directory")
			}
			if len(config.Shares) != 0 {
				t.Fatal("environment-only configuration must not bootstrap shares")
			}
			config.StateDir = filepath.Join(t.TempDir(), "state")
			app, err := New(config, t.TempDir())
			if app != nil {
				defer app.Close()
			}
			if password == "" {
				if err == nil || !strings.Contains(err.Error(), "exactly one") {
					t.Fatalf("missing credentials must fail in New: %v", err)
				}
			} else if len(password) < 6 {
				if err == nil || !strings.Contains(err.Error(), "at least 6") {
					t.Fatalf("short credentials must fail in New: %v", err)
				}
			} else if err != nil {
				t.Fatalf("environment credentials with zero bootstrap shares must start: %v", err)
			}
		})
	}
}

func TestLoadConfigEnvironmentUmask(t *testing.T) {
	for _, value := range []string{"000", "022", "777", "0000", "0022", "0777"} {
		t.Run(value, func(t *testing.T) {
			clearConfigEnvironment(t)
			t.Setenv("UMASK", value)
			config, err := LoadConfig("")
			if err != nil || config.Umask != value {
				t.Fatalf("valid environment UMASK was not preserved: %v", err)
			}
		})
	}
}

func TestLoadConfigInvalidEnvironmentUmask(t *testing.T) {
	for _, value := range []string{"", "0", "22", "00000", "0088", "0999", "1000", "1777", "7777", "-022", "+022", "+22", "0o22", " 022", "022\n", "private-password-marker"} {
		t.Run("UMASK="+value, func(t *testing.T) {
			clearConfigEnvironment(t)
			t.Setenv("PASSWORD", "private-password-marker")
			t.Setenv("UMASK", value)
			_, err := LoadConfig("")
			if err == nil || !strings.Contains(err.Error(), "UMASK") {
				t.Fatalf("invalid environment must identify UMASK: %v", err)
			}
			if strings.Contains(err.Error(), "private-password-marker") {
				t.Fatal("configuration error leaked sensitive environment content")
			}
		})
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	for _, contents := range []string{
		"", "null", "[]", "{", "{} {}", "{} trailing", `{"port":"8080"}`,
		`{"secureCookies":"false"}`, `{"umask":22}`, `{"umask":""}`, `{"unknown":true}`, `{"allowedRoots":[]}`,
	} {
		t.Run(contents, func(t *testing.T) {
			clearConfigEnvironment(t)
			path := writeLoaderConfig(t, contents)
			// A selected file must fail rather than fall back to valid environment values.
			t.Setenv("PASSWORD", "env-password")
			t.Setenv("UMASK", "0022")
			if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("invalid JSON must identify its file: %v", err)
			}
		})
	}
}

func TestLoadConfigFilePreservesExplicitZeroValues(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("PASSWORD", "env-password")
	t.Setenv("UMASK", "0077")
	config, err := LoadConfig(writeLoaderConfig(t, `{"ip":"","port":0,"stateDir":"","password":"","passwordFile":"","passwordSourceFile":"","secureCookies":false,"shares":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.IP != "" || config.Port != 0 || config.StateDir != "" || config.Password != "" || config.PasswordFile != "" || config.PasswordSourceFile != "" || config.SecureCookies || len(config.Shares) != 0 {
		t.Fatal("explicit JSON zero values must not be replaced by defaults or environment")
	}
}

func TestLoadConfigInvalidEnvironmentStateDirectory(t *testing.T) {
	rootLink := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(string(filepath.Separator), rootLink); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "state", "./state", "../state", "private-password-marker", "/", "/tmp/..", rootLink} {
		t.Run("ConfigPath="+value, func(t *testing.T) {
			clearConfigEnvironment(t)
			t.Setenv("PASSWORD", "private-password-marker")
			t.Setenv("ConfigPath", value)
			_, err := LoadConfig("")
			if err == nil || !strings.Contains(err.Error(), "ConfigPath") {
				t.Fatalf("invalid state directory must identify ConfigPath: %v", err)
			}
			if strings.Contains(err.Error(), "private-password-marker") {
				t.Fatal("configuration error leaked sensitive environment content")
			}
		})
	}
}

func TestLoadConfigFileIgnoresConfigPath(t *testing.T) {
	clearConfigEnvironment(t)
	path := writeLoaderConfig(t, `{"stateDir":"/file-state","password":"file-password"}`)
	for _, value := range []string{"", "relative", "/", "/tmp/..", filepath.Join(t.TempDir(), "unused state"), path} {
		t.Run("ConfigPath="+value, func(t *testing.T) {
			t.Setenv("ConfigPath", value)
			config, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if config.StateDir != "/file-state" || config.Password != "file-password" {
				t.Fatal("ConfigPath must not override file configuration")
			}
		})
	}
}

func TestLoadConfigEnvironmentStateDirectoryPersistsSettings(t *testing.T) {
	clearConfigEnvironment(t)
	state := filepath.Join(t.TempDir(), "saved state")
	t.Setenv("ConfigPath", state)
	t.Setenv("PASSWORD", "123123")
	config, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	f := &fileSettingsFixture{t: t, config: config, static: t.TempDir()}
	t.Cleanup(f.stop)
	f.start()
	if raw := f.request("GET", "/api/settings/shares", "", http.StatusOK); string(raw) != "{\"shares\":[]}\n" {
		t.Fatalf("environment configuration must start without shares: %s", raw)
	}
	sharePath := t.TempDir()
	shareJSON, err := json.Marshal(Share{Name: "Saved share", Path: sharePath, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/settings/shares/save", string(shareJSON), http.StatusOK)
	var saved struct {
		Shares []Share `json:"shares"`
	}
	if err := json.Unmarshal(f.request("GET", "/api/settings/shares", "", http.StatusOK), &saved); err != nil {
		t.Fatal(err)
	}
	canonicalShare, err := filepath.EvalSymlinks(sharePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Shares) != 1 || saved.Shares[0].ID == "" || saved.Shares[0].Name != "Saved share" || saved.Shares[0].Path != canonicalShare || !saved.Shares[0].ReadOnly {
		t.Fatalf("share was not saved: %+v", saved.Shares)
	}
	assertFileViewDocument(t, f.request("POST", "/api/settings/files/save", customFileViewDocument, http.StatusOK), customFileViewDocument)
	database, err := os.Stat(filepath.Join(state, "state.sqlite"))
	if err != nil {
		t.Fatalf("database was not created under ConfigPath: %v", err)
	}
	f.stop()
	f.config, err = LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	f.start()
	reopened, err := os.Stat(filepath.Join(state, "state.sqlite"))
	if err != nil || !os.SameFile(database, reopened) {
		t.Fatalf("restart did not reuse the ConfigPath database: %v", err)
	}
	var restored struct {
		Shares []Share `json:"shares"`
	}
	if err := json.Unmarshal(f.request("GET", "/api/settings/shares", "", http.StatusOK), &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Shares, saved.Shares) {
		t.Fatalf("shares changed across environment reload: got %+v, want %+v", restored.Shares, saved.Shares)
	}
	assertFileViewDocument(t, f.request("GET", "/api/settings/files", "", http.StatusOK), customFileViewDocument)
}

func TestLoadConfigEnvironmentStateDirectoryIsNotConfigurationFile(t *testing.T) {
	clearConfigEnvironment(t)
	contents := `{"password":"json-password","stateDir":"/json-state"}`
	path := writeLoaderConfig(t, contents)
	t.Setenv("ConfigPath", path)
	t.Setenv("PASSWORD", "123123")
	config, err := LoadConfig("")
	if err != nil {
		t.Fatalf("ConfigPath must not be decoded as JSON: %v", err)
	}
	if config.StateDir != path || config.Password != "123123" {
		t.Fatal("ConfigPath file contents must not populate configuration")
	}
	app, err := New(config, t.TempDir())
	if app != nil {
		app.Close()
	}
	if err == nil {
		t.Fatal("a regular file cannot be used as the state directory")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != contents {
		t.Fatalf("attempted startup modified the ConfigPath file: %v", err)
	}
}
