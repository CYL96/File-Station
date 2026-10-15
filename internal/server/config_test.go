package server

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	app, err := New(Config{IP: "127.0.0.1", Port: 8080, StateDir: alias, PasswordFile: secret, Shares: []Share{{ID: "files", Name: "Files", Path: share}}}, base)
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
			app, err := New(Config{IP: test.ip, Port: test.port, Password: test.password, PasswordFile: test.passwordFile, StateDir: filepath.Join(base, "state"), Shares: []Share{{Name: "Files", Path: share}}}, base)
			if app != nil {
				app.Close()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestRejectBootstrapSharesContainingCredentialSource(t *testing.T) {
	for _, private := range []string{"source", "credential"} {
		t.Run(private, func(t *testing.T) {
			base := t.TempDir()
			paths := map[string]string{}
			for _, name := range []string{"source", "credential"} {
				dir := filepath.Join(base, name)
				if err := os.Mkdir(dir, 0750); err != nil {
					t.Fatal(err)
				}
				paths[name] = filepath.Join(dir, "password")
				if err := os.WriteFile(paths[name], []byte("correct-password"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			state := filepath.Join(base, "state")
			app, err := New(Config{
				IP: "127.0.0.1", Port: 8080, StateDir: state,
				PasswordFile: paths["credential"], PasswordSourceFile: paths["source"],
				Shares: []Share{{ID: "private", Name: "Private", Path: filepath.Dir(paths[private])}},
			}, base)
			if app != nil {
				app.Close()
			}
			assertTextStatus(t, err, 403)
			if _, err := os.Stat(filepath.Join(state, "state.sqlite")); !os.IsNotExist(err) {
				t.Fatalf("rejected bootstrap created database: %v", err)
			}
		})
	}
}

func TestInaccessibleCredentialSourceStillStarts(t *testing.T) {
	const childEnv = "FILE_STATION_TEST_PRIVATE_SOURCE"
	check := func(base string) {
		t.Helper()
		source := filepath.Join(base, "source-alias", "private", "password")
		if _, err := os.ReadFile(source); !os.IsPermission(err) {
			t.Fatalf("original source must really be inaccessible: %v", err)
		}
		app, err := New(Config{
			IP: "127.0.0.1", Port: 8080, StateDir: filepath.Join(base, "state"),
			PasswordFile: filepath.Join(base, "credential"), PasswordSourceFile: source,
			Shares: []Share{{ID: "data", Name: "Data", Path: filepath.Join(base, "data")}},
		}, base)
		if err != nil {
			t.Fatalf("unreadable original source prevented startup: %v", err)
		}
		defer app.Close()
		for _, p := range []string{filepath.Join(base, "source"), filepath.Join(base, "source-alias")} {
			sh, err := app.openShare(Share{ID: "private", Name: "Private", Path: p})
			if sh.root != nil {
				sh.root.Close()
			}
			assertTextStatus(t, err, 403)
		}
		if len(app.shares) != 1 || app.shares[0].root == nil {
			t.Fatal("independent data directory was not shared")
		}
	}
	if base := os.Getenv(childEnv); base != "" {
		check(base)
		return
	}
	// Re-execute under an unprivileged identity when the test runner is root.
	// A public temporary parent avoids t.TempDir's private ancestor permissions.
	base, err := os.MkdirTemp("", "file-station-private-source-")
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(base, "source", "private")
	t.Cleanup(func() {
		_ = os.Chmod(private, 0700)
		_ = os.RemoveAll(base)
	})
	for _, p := range []string{base, filepath.Join(base, "source"), private, filepath.Join(base, "data"), filepath.Join(base, "state")} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(private, "password"), []byte("original-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "credential"), []byte("credential-password"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(base, "credential"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "source"), filepath.Join(base, "source-alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(private, 0000); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		check(base)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	childPath := filepath.Join(base, "server.test")
	child, err := os.OpenFile(childPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(child, original)
	closeErr := child.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := os.Chmod(childPath, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(childPath, "-test.run=^TestInaccessibleCredentialSourceStillStarts$")
	cmd.Dir = base
	cmd.Env = append(os.Environ(), childEnv+"="+base)
	if err := os.Chown(filepath.Join(base, "state"), 65534, 65534); err != nil {
		t.Fatal(err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("inaccessible source regression failed: %v\n%s", err, output)
	}
}
