package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func textRead(t *testing.T, app *Server, name string) (string, string) {
	t.Helper()
	w := httptest.NewRecorder()
	if err := app.fileText(w, httptest.NewRequest("GET", "/api/files/text?share=files&path="+url.QueryEscape(name), nil)); err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body["content"], body["version"]
}

func textSave(t *testing.T, app *Server, name, content, version string, ctx context.Context) (string, error) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"share": "files", "path": name, "content": content, "version": version})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/files/text/save", bytes.NewReader(body))
	if ctx != nil {
		r = r.WithContext(ctx)
	}
	w := httptest.NewRecorder()
	if err = app.saveFileText(w, r); err != nil {
		return "", err
	}
	var response map[string]string
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response["version"], nil
}

func assertTextStatus(t *testing.T, err error, status int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected status %d, got success", status)
	}
	w := httptest.NewRecorder()
	errorResponse(w, err)
	if w.Code != status {
		t.Fatalf("status = %d, want %d: %v", w.Code, status, err)
	}
}

func assertTextDisk(t *testing.T, root, name, content string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || string(got) != content {
		t.Fatalf("disk contents = %q, err = %v; want %q", got, err, content)
	}
	assertNoTextTemps(t, root)
}

func assertNoTextTemps(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".webui-text-") {
			t.Errorf("temporary file leaked: %s", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTextRoundTripAndVersions(t *testing.T) {
	app, root := newMetadataServer(t)
	name := "notes.txt"
	if err := os.WriteFile(filepath.Join(root, name), []byte("original"), 0640); err != nil {
		t.Fatal(err)
	}
	_, version := textRead(t, app, name)
	for _, content := range []string{"", "中文与 emoji 😀\r\n第二行\r\n", "plain\n", "\t\r\n"} {
		previous := version
		var err error
		version, err = textSave(t, app, name, content, version, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, readVersion := textRead(t, app, name)
		if got != content || version != readVersion || version == previous {
			t.Fatalf("roundtrip = %q version %q; want %q version %q (old %q)", got, readVersion, content, version, previous)
		}
		assertTextDisk(t, root, name, content)
		_, err = textSave(t, app, name, "stale draft", previous, nil)
		assertTextStatus(t, err, 409)
		assertTextDisk(t, root, name, content)
	}
}

func TestTextEscapedJSONAndSizeBoundary(t *testing.T) {
	app, root := newMetadataServer(t)
	name := "boundary.txt"
	if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, version := textRead(t, app, name)
	// Six JSON bytes per one raw byte, exceeding the generic decode limit.
	body := `{"share":"files","path":"boundary.txt","version":"` + version + `","content":"` + strings.Repeat(`\u0009`, textFileLimit) + `"}`
	w := httptest.NewRecorder()
	if err := app.saveFileText(w, httptest.NewRequest("POST", "/api/files/text/save", strings.NewReader(body))); err != nil {
		t.Fatal(err)
	}
	content, version := textRead(t, app, name)
	if len(content) != textFileLimit || content != strings.Repeat("\t", textFileLimit) {
		t.Fatal("escaped boundary was not preserved")
	}
	_, err := textSave(t, app, name, strings.Repeat("a", textFileLimit+1), version, nil)
	assertTextStatus(t, err, 415)
	assertTextDisk(t, root, name, content)
	if err = os.WriteFile(filepath.Join(root, name), []byte(strings.Repeat("a", textFileLimit+1)), 0600); err != nil {
		t.Fatal(err)
	}
	err = app.fileText(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/files/text?share=files&path=boundary.txt", nil))
	assertTextStatus(t, err, 415)
	assertNoTextTemps(t, root)
}

func TestTextBinaryAndMalformedRequests(t *testing.T) {
	app, root := newMetadataServer(t)
	for name, content := range map[string][]byte{"nul.txt": {'a', 0, 'b'}, "invalid.txt": {0xff, 0xfe}} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0600); err != nil {
			t.Fatal(err)
		}
		err := app.fileText(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/files/text?share=files&path="+name, nil))
		assertTextStatus(t, err, 415)
		assertTextDisk(t, root, name, string(content))
	}
	if err := os.WriteFile(filepath.Join(root, "valid.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, version := textRead(t, app, "valid.txt")
	_, err := textSave(t, app, "valid.txt", "bad\x00text", version, nil)
	assertTextStatus(t, err, 415)
	for _, body := range []string{
		`{"share":"files","path":"valid.txt","version":"` + version + `"}`,
		`{"share":"files","path":"valid.txt","version":"` + version + `","content":null}`,
		`{"share":"files","path":"valid.txt","version":"` + version + `","content":"new","extra":true}`,
		`{"share":"files","path":"valid.txt","version":"` + version + `","content":"new"} {}`,
	} {
		err = app.saveFileText(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/files/text/save", strings.NewReader(body)))
		assertTextStatus(t, err, 400)
		assertTextDisk(t, root, "valid.txt", "keep")
	}
	invalidJSON := []byte(`{"share":"files","path":"valid.txt","version":"` + version + `","content":"`)
	invalidJSON = append(invalidJSON, 0xff, '"', '}')
	err = app.saveFileText(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/files/text/save", bytes.NewReader(invalidJSON)))
	assertTextStatus(t, err, 415)
	assertTextDisk(t, root, "valid.txt", "keep")
}

func TestTextConflictsAndMissingFiles(t *testing.T) {
	for _, change := range []string{"content", "same-content-replacement", "metadata", "missing", "binary"} {
		t.Run(change, func(t *testing.T) {
			app, root := newMetadataServer(t)
			name := "conflict.txt"
			absolute := filepath.Join(root, name)
			if err := os.WriteFile(absolute, []byte("original"), 0640); err != nil {
				t.Fatal(err)
			}
			_, version := textRead(t, app, name)
			want := "original"
			switch change {
			case "content", "binary":
				want = "external"
				if change == "binary" {
					want = "external\x00binary"
				}
				if err := os.WriteFile(absolute, []byte(want), 0640); err != nil {
					t.Fatal(err)
				}
			case "same-content-replacement":
				if err := os.WriteFile(filepath.Join(root, "replacement"), []byte(want), 0640); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(root, "replacement"), absolute); err != nil {
					t.Fatal(err)
				}
			case "metadata":
				if err := os.Chmod(absolute, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(absolute); err != nil {
					t.Fatal(err)
				}
			}
			_, err := textSave(t, app, name, "draft", version, nil)
			assertTextStatus(t, err, 409)
			if change == "missing" {
				if _, err = os.Stat(absolute); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing target recreated: %v", err)
				}
				assertNoTextTemps(t, root)
			} else {
				assertTextDisk(t, root, name, want)
			}
		})
	}
}

func TestTextRejectsUnsafeAndSpecialPaths(t *testing.T) {
	app, root := newMetadataServer(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"link", "linked-dir/secret", "folder", "fifo", "../secret", ".webui-private"} {
		t.Run(name, func(t *testing.T) {
			err := app.fileText(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/files/text?share=files&path="+url.QueryEscape(name), nil))
			assertTextStatus(t, err, 403)
			_, err = textSave(t, app, name, "draft", strings.Repeat("a", 64), nil)
			assertTextStatus(t, err, 403)
		})
	}
	err := app.fileText(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/files/text?share=files&path=missing", nil))
	assertTextStatus(t, err, 404)
	assertTextDisk(t, outside, "secret", "secret")
	assertNoTextTemps(t, root)
}

func TestTextReadOnlyAndHostPermissions(t *testing.T) {
	app, root := newMetadataServer(t)
	if err := os.WriteFile(filepath.Join(root, "readonly.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, version := textRead(t, app, "readonly.txt")
	app.shares[0].ReadOnly = true
	textRead(t, app, "readonly.txt")
	_, err := textSave(t, app, "readonly.txt", "draft", version, nil)
	assertTextStatus(t, err, 403)
	assertTextDisk(t, root, "readonly.txt", "keep")
	app.shares[0].ReadOnly = false
	if os.Geteuid() == 0 {
		t.Skip("root has DAC override; read-only share was checked")
	}
	if err = os.Chmod(filepath.Join(root, "readonly.txt"), 0400); err != nil {
		t.Fatal(err)
	}
	_, version = textRead(t, app, "readonly.txt")
	_, err = textSave(t, app, "readonly.txt", "draft", version, nil)
	assertTextStatus(t, err, 403)
	assertTextDisk(t, root, "readonly.txt", "keep")
}

func TestTextPreservesModeOwnershipAndXattrs(t *testing.T) {
	app, root := newMetadataServer(t)
	absolute := filepath.Join(root, "metadata.txt")
	if err := os.WriteFile(absolute, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(absolute, 10001, 10002); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(absolute, 0750|os.ModeSetuid|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	attrSupported := true
	if err := unix.Setxattr(absolute, "user.webui-test", []byte("attribute\x00value"), 0); err != nil {
		if !errors.Is(err, unix.ENOTSUP) {
			t.Fatal(err)
		}
		attrSupported = false
	}
	var before unix.Stat_t
	if err := unix.Stat(absolute, &before); err != nil {
		t.Fatal(err)
	}
	_, version := textRead(t, app, "metadata.txt")
	_, err := textSave(t, app, "metadata.txt", "after", version, nil)
	if !attrSupported {
		assertTextStatus(t, err, 403)
		assertTextDisk(t, root, "metadata.txt", "before")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var after unix.Stat_t
	if err = unix.Stat(absolute, &after); err != nil {
		t.Fatal(err)
	}
	if after.Mode&07777 != before.Mode&07777 || after.Uid != before.Uid || after.Gid != before.Gid || after.Ino == before.Ino {
		t.Fatalf("metadata/atomic replacement before=%+v after=%+v", before, after)
	}
	buf := make([]byte, 256)
	n, err := unix.Getxattr(absolute, "user.webui-test", buf)
	if err != nil || string(buf[:n]) != "attribute\x00value" {
		t.Fatalf("xattr = %q: %v", buf[:n], err)
	}
	assertTextDisk(t, root, "metadata.txt", "after")
}

func TestTextPreservesPOSIXACL(t *testing.T) {
	app, root := newMetadataServer(t)
	absolute := filepath.Join(root, "acl.txt")
	if err := os.WriteFile(absolute, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	// Linux POSIX ACL version 2: owner rw, named user r, group -, mask r,
	// other -. A nontrivial named-user ACL must survive the inode replacement.
	acl := make([]byte, 4+5*8)
	binary.LittleEndian.PutUint32(acl, 2)
	entries := []struct {
		tag, perm uint16
		id        uint32
	}{
		{1, 6, ^uint32(0)}, {2, 4, uint32(os.Getuid()) + 10000},
		{4, 0, ^uint32(0)}, {16, 4, ^uint32(0)}, {32, 0, ^uint32(0)},
	}
	for i, entry := range entries {
		at := 4 + i*8
		binary.LittleEndian.PutUint16(acl[at:], entry.tag)
		binary.LittleEndian.PutUint16(acl[at+2:], entry.perm)
		binary.LittleEndian.PutUint32(acl[at+4:], entry.id)
	}
	if err := unix.Setxattr(absolute, "system.posix_acl_access", acl, 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EPERM) {
			t.Skipf("filesystem cannot set POSIX ACLs: %v", err)
		}
		t.Fatal(err)
	}
	_, version := textRead(t, app, "acl.txt")
	if _, err := textSave(t, app, "acl.txt", "after", version, nil); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(acl))
	n, err := unix.Getxattr(absolute, "system.posix_acl_access", got)
	if err != nil || !bytes.Equal(got[:n], acl) {
		t.Fatalf("ACL changed: %x, err %v; want %x", got[:n], err, acl)
	}
	assertTextDisk(t, root, "acl.txt", "after")
}

// The handler checks Context.Err while streaming the temporary file. Hooking
// that boundary makes external changes/cancellation deterministic, not timed.
type textBoundaryContext struct {
	context.Context
	root string
	once sync.Once
	hook func()
}

func (ctx *textBoundaryContext) Err() error {
	entries, _ := os.ReadDir(ctx.root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".webui-text-") {
			ctx.once.Do(ctx.hook)
			break
		}
	}
	return ctx.Context.Err()
}

func TestTextSecondCheckAndTemporaryCleanup(t *testing.T) {
	for _, operation := range []string{"external-write", "external-replacement", "parent-replacement", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			app, root := newMetadataServer(t)
			folder := filepath.Join(root, "folder")
			if err := os.Mkdir(folder, 0750); err != nil {
				t.Fatal(err)
			}
			absolute := filepath.Join(folder, "file.txt")
			if err := os.WriteFile(absolute, []byte("original"), 0640); err != nil {
				t.Fatal(err)
			}
			_, version := textRead(t, app, "folder/file.txt")
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &textBoundaryContext{Context: base, root: folder}
			ctx.hook = func() {
				switch operation {
				case "external-write":
					if err := os.WriteFile(absolute, []byte("external"), 0640); err != nil {
						t.Fatal(err)
					}
				case "external-replacement":
					if err := os.WriteFile(filepath.Join(folder, "replacement"), []byte("external"), 0640); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(filepath.Join(folder, "replacement"), absolute); err != nil {
						t.Fatal(err)
					}
				case "parent-replacement":
					if err := os.Rename(folder, filepath.Join(root, "old-folder")); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(folder, 0750); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(absolute, []byte("external"), 0640); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				}
			}
			_, err := textSave(t, app, "folder/file.txt", "draft", version, ctx)
			if operation == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation = %v", err)
				}
				assertTextDisk(t, root, "folder/file.txt", "original")
			} else {
				assertTextStatus(t, err, 409)
				assertTextDisk(t, root, "folder/file.txt", "external")
				if operation == "parent-replacement" {
					assertTextDisk(t, root, "old-folder/file.txt", "original")
				}
			}
			assertNoTextTemps(t, root)
		})
	}
}

type textWaitingContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (ctx *textWaitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.entered) })
	return ctx.Context.Done()
}

func TestTextCancellationWhileWaitingForMutation(t *testing.T) {
	app, root := newMetadataServer(t)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, version := textRead(t, app, "file.txt")
	body, err := json.Marshal(map[string]string{"share": "files", "path": "file.txt", "content": "draft", "version": version})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := &textWaitingContext{Context: ctx, entered: make(chan struct{})}
	app.mutation.Lock()
	defer app.mutation.Unlock()
	done := make(chan error, 1)
	go func() {
		done <- app.saveFileText(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/files/text/save", bytes.NewReader(body)).WithContext(waiting))
	}()
	select {
	case <-waiting.entered:
	case <-time.After(time.Second):
		t.Fatal("save did not reach the held mutation lock")
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled save remained blocked on mutation lock")
	}
	assertTextDisk(t, root, "file.txt", "keep")
}

func TestTextRoutesAuthenticationAndCSRF(t *testing.T) {
	app, root := newMetadataServer(t)
	if err := os.WriteFile(filepath.Join(root, "route.txt"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	request := func(method, target string, body io.Reader, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, body)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/files/text?share=files&path=route.txt", nil, nil, ""); w.Code != 401 {
		t.Fatalf("unauthenticated GET = %d", w.Code)
	}
	if w := request("POST", "/api/files/text/save", strings.NewReader(`{}`), nil, ""); w.Code != 401 {
		t.Fatalf("unauthenticated POST = %d", w.Code)
	}
	login := request("POST", "/api/login", strings.NewReader(`{"password":"metadata-password"}`), nil, "")
	if login.Code != 200 {
		t.Fatalf("login = %d: %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %v", cookies)
	}
	var session map[string]string
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	get := request("GET", "/api/files/text?share=files&path=route.txt", nil, cookies[0], "")
	if get.Code != 200 || get.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET = %d: %s", get.Code, get.Body.String())
	}
	var text map[string]string
	if err := json.Unmarshal(get.Body.Bytes(), &text); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"share": "files", "path": "route.txt", "content": "after", "version": text["version"]})
	if err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/files/text/save", bytes.NewReader(body), cookies[0], "wrong"); w.Code != 403 {
		t.Fatalf("missing CSRF = %d: %s", w.Code, w.Body.String())
	}
	assertTextDisk(t, root, "route.txt", "before")
	if w := request("POST", "/api/files/text/save", bytes.NewReader(body), cookies[0], session["csrf"]); w.Code != 200 {
		t.Fatalf("POST = %d: %s", w.Code, w.Body.String())
	}
	assertTextDisk(t, root, "route.txt", "after")
}

func TestTextRefusesHardlinkedSave(t *testing.T) {
	app, root := newMetadataServer(t)
	if err := os.WriteFile(filepath.Join(root, "first.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "first.txt"), filepath.Join(root, "second.txt")); err != nil {
		t.Fatal(err)
	}
	_, version := textRead(t, app, "first.txt")
	_, err := textSave(t, app, "first.txt", "draft", version, nil)
	assertTextStatus(t, err, 403)
	assertTextDisk(t, root, "first.txt", "keep")
	assertTextDisk(t, root, "second.txt", "keep")
	first, err := os.Stat(filepath.Join(root, "first.txt"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(filepath.Join(root, "second.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(first, second) {
		t.Fatal("failed save severed the hardlink relationship")
	}
}
