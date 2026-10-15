package server

import (
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
