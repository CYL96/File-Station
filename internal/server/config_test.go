package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectStateSymlinkInsideShare(t *testing.T) {
	base := t.TempDir()
	share := filepath.Join(base, "share")
	private := filepath.Join(share, "private")
	if err := os.MkdirAll(private, 0750); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "state-alias")
	if err := os.Symlink(private, alias); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(base, "password")
	if err := os.WriteFile(secret, []byte("correct-password-123"), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := New(Config{IP: "127.0.0.1", Port: 8080, StateDir: alias, PasswordFile: secret, AllowedRoots: []AllowedRoot{{Path: share}}, Shares: []Share{{ID: "files", Name: "Files", Path: share}}}, base)
	if app != nil {
		app.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "stateDir must be outside shares") {
		t.Fatalf("private database exposed by share: %v", err)
	}
	if _, err = os.Stat(filepath.Join(private, "state.sqlite")); !os.IsNotExist(err) {
		t.Fatal("rejected config created exposed database")
	}
}

func TestRejectInvalidConfiguration(t *testing.T) {
	base := t.TempDir()
	share := filepath.Join(base, "share")
	if err := os.Mkdir(share, 0750); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name         string
		ip           string
		port         int
		password     string
		passwordFile string
		want         string
	}{
		{"invalid IP", "localhost", 8080, "123123", "", "ip must be"},
		{"zero port", "127.0.0.1", 0, "123123", "", "port must be"},
		{"negative port", "127.0.0.1", -1, "123123", "", "port must be"},
		{"large port", "::1", 65536, "123123", "", "port must be"},
		{"missing password", "127.0.0.1", 8080, "", "", "exactly one"},
		{"ambiguous password", "127.0.0.1", 8080, "123123", filepath.Join(base, "password"), "exactly one"},
		{"short password", "127.0.0.1", 8080, "12345", "", "at least 6 bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, err := New(Config{IP: test.ip, Port: test.port, Password: test.password, PasswordFile: test.passwordFile, StateDir: filepath.Join(base, "state"), AllowedRoots: []AllowedRoot{{Path: share}}, Shares: []Share{{Name: "Files", Path: share}}}, base)
			if app != nil {
				app.Close()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}
