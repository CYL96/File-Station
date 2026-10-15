package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type umaskFixture struct {
	app    *Server
	root   string
	cookie *http.Cookie
	csrf   string
}

func newUmaskFixture(t *testing.T, mask string) *umaskFixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "files")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	app, err := New(Config{IP: "127.0.0.1", Port: 8080, StateDir: filepath.Join(base, "state"), Umask: mask, Password: "correct-password-123", Shares: []Share{{ID: "files", Name: "Files", Path: root}}}, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	f := &umaskFixture{app: app, root: root}
	w := f.request(t, "POST", "/api/login", map[string]string{"password": "correct-password-123"}, 200)
	var login map[string]string
	if err = json.Unmarshal(w.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	f.csrf = login["csrf"]
	f.cookie = w.Result().Cookies()[0]
	return f
}

func (f *umaskFixture) authorize(r *http.Request) {
	if f.cookie != nil {
		r.AddCookie(f.cookie)
	}
	r.Header.Set("X-CSRF-Token", f.csrf)
}

func (f *umaskFixture) request(t *testing.T, method, route string, body any, want int) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if text, ok := body.(string); ok {
		reader = strings.NewReader(text)
	} else if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	r := httptest.NewRequest(method, route, reader)
	f.authorize(r)
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, route, w.Code, want, w.Body.String())
	}
	return w
}

func (f *umaskFixture) operation(t *testing.T, kind, name, destination string) {
	t.Helper()
	w := f.request(t, "POST", "/api/operation", map[string]any{"kind": kind, "share": "files", "paths": []string{name}, "destination": destination}, 200)
	var started map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var tasks []Task
		w = f.request(t, "GET", "/api/tasks", nil, 200)
		if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			if task.ID == started["id"] && task.Status != "running" {
				if task.Status != "done" {
					t.Fatalf("operation failed: %+v", task)
				}
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not finish")
}

func assertUmaskMode(t *testing.T, name string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", name, got, want)
	}
}

func TestUmaskHTTPFileLifecycle(t *testing.T) {
	for _, tc := range []struct {
		mask string
		file os.FileMode
		dir  os.FileMode
	}{{"0022", 0644, 0755}, {"0002", 0664, 0775}, {"0077", 0600, 0700}} {
		t.Run(tc.mask, func(t *testing.T) {
			f := newUmaskFixture(t, tc.mask)
			f.request(t, "POST", "/api/mkdir", map[string]string{"share": "files", "name": "source"}, 200)
			f.request(t, "POST", "/api/mkdir", map[string]string{"share": "files", "path": "source", "name": "nested"}, 200)
			f.request(t, "POST", "/api/mkdir", map[string]string{"share": "files", "name": "target"}, 200)
			f.request(t, "POST", "/api/upload?share=files&path=source/nested&name=note.txt", "original", 200)
			f.operation(t, "copy", "source", "target")
			for _, name := range []string{"source", "source/nested", "target", "target/source", "target/source/nested"} {
				assertUmaskMode(t, filepath.Join(f.root, name), tc.dir)
			}
			for _, name := range []string{"source/nested/note.txt", "target/source/nested/note.txt"} {
				assertUmaskMode(t, filepath.Join(f.root, name), tc.file)
			}
			// Copying one regular file also passes through the private container.
			f.operation(t, "copy", "source/nested/note.txt", "target")
			assertUmaskMode(t, filepath.Join(f.root, "target/note.txt"), tc.file)
			original := filepath.Join(f.root, "source/nested/note.txt")
			if err := os.Chmod(original, 0640); err != nil {
				t.Fatal(err)
			}
			var text map[string]string
			w := f.request(t, "GET", "/api/files/text?share=files&path=source/nested/note.txt", nil, 200)
			if err := json.Unmarshal(w.Body.Bytes(), &text); err != nil {
				t.Fatal(err)
			}
			f.request(t, "POST", "/api/files/text/save", map[string]string{"share": "files", "path": "source/nested/note.txt", "content": "edited", "version": text["version"]}, 200)
			assertUmaskMode(t, original, 0640)
			f.request(t, "POST", "/api/rename", map[string]string{"share": "files", "path": "source/nested/note.txt", "name": "renamed.txt"}, 200)
			f.operation(t, "move", "source/nested/renamed.txt", "target")
			assertUmaskMode(t, filepath.Join(f.root, "target/renamed.txt"), 0640)
			f.request(t, "POST", "/api/mkdir", map[string]string{"share": "files", "path": "source", "name": "missing"}, 200)
			f.request(t, "POST", "/api/mkdir", map[string]string{"share": "files", "path": "source/missing", "name": "deep"}, 200)
			f.operation(t, "move", "target/renamed.txt", "source/missing/deep")

			// Restoring missing intermediate ancestors must not chmod existing ones.
			if err := os.Chmod(filepath.Join(f.root, "source"), 0711); err != nil {
				t.Fatal(err)
			}
			f.operation(t, "trash", "source/missing/deep/renamed.txt", "")
			var trash []TrashEntry
			w = f.request(t, "GET", "/api/trash?share=files", nil, 200)
			if err := json.Unmarshal(w.Body.Bytes(), &trash); err != nil || len(trash) != 1 {
				t.Fatalf("trash = %+v: %v", trash, err)
			}
			if err := os.RemoveAll(filepath.Join(f.root, "source/missing")); err != nil {
				t.Fatal(err)
			}
			assertUmaskMode(t, filepath.Join(f.root, ".webui-trash"), 0700)
			assertUmaskMode(t, filepath.Join(f.root, ".webui-trash", trash[0].ID), 0700)
			f.request(t, "POST", "/api/trash/restore", map[string]string{"share": "files", "id": trash[0].ID}, 200)
			assertUmaskMode(t, filepath.Join(f.root, "source"), 0711)
			assertUmaskMode(t, filepath.Join(f.root, "source/missing"), tc.dir)
			assertUmaskMode(t, filepath.Join(f.root, "source/missing/deep"), tc.dir)
			assertUmaskMode(t, filepath.Join(f.root, "source/missing/deep/renamed.txt"), 0640)
			assertUmaskMode(t, f.app.config.StateDir, 0700)
			assertUmaskMode(t, filepath.Join(f.app.config.StateDir, "state.sqlite"), 0600)
		})
	}
}

func TestNewRejectsInvalidUmask(t *testing.T) {
	for _, mask := range []string{"0", "22", "00022", "0088", "078", "1000", "07777", "-022", "+022", " 022", "022\n"} {
		t.Run(mask, func(t *testing.T) {
			_, err := New(Config{Umask: mask}, "")
			if err == nil || !strings.Contains(err.Error(), "umask") {
				t.Fatalf("invalid umask accepted or not validated first: %v", err)
			}
		})
	}
}

type umaskUploadReader struct {
	ctx     context.Context
	entered chan struct{}
	once    sync.Once
}

func (r *umaskUploadReader) Read(p []byte) (int, error) {
	first := false
	r.once.Do(func() { first = true })
	if first {
		return copy(p, "partial confidential upload"), nil
	}
	close(r.entered)
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func TestUmaskUploadStaysPrivateUntilComplete(t *testing.T) {
	f := newUmaskFixture(t, "0002")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &umaskUploadReader{ctx: ctx, entered: make(chan struct{})}
	r := httptest.NewRequest("POST", "/api/upload?share=files&name=secret.txt", body).WithContext(ctx)
	f.authorize(r)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.app.ServeHTTP(httptest.NewRecorder(), r)
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-body.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not reach partial stream")
	}
	entries, err := os.ReadDir(f.root)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), ".webui-upload-") {
		t.Fatalf("upload stage = %+v: %v", entries, err)
	}
	stage := filepath.Join(f.root, entries[0].Name())
	assertUmaskMode(t, stage, 0600)
	if data, err := os.ReadFile(stage); err != nil || string(data) != "partial confidential upload" {
		t.Fatalf("private upload inaccessible to its owner: %q, %v", data, err)
	}
	cancel()
	<-done
	entries, err = os.ReadDir(f.root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled upload left nodes: %+v, %v", entries, err)
	}
}

func TestUmaskCopyCanPublishWithoutOwnerPermissions(t *testing.T) {
	f := newUmaskFixture(t, "0777")
	if err := os.MkdirAll(filepath.Join(f.root, "source/nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "source/nested/note.txt"), []byte("complete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.root, "target"), 0700); err != nil {
		t.Fatal(err)
	}
	f.operation(t, "copy", "source", "target")
	for _, name := range []string{"target/source", "target/source/nested", "target/source/nested/note.txt"} {
		absolute := filepath.Join(f.root, name)
		assertUmaskMode(t, absolute, 0000)
		// Restore traversal after checking each directory so the next assertion
		// and test cleanup also work for an unprivileged test runner.
		info, err := os.Stat(absolute)
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			if err := os.Chmod(absolute, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
}
