package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func newMetadataServer(t *testing.T) (*Server, string) {
	t.Helper()
	base := t.TempDir()
	share := filepath.Join(base, "files")
	if err := os.Mkdir(share, 0750); err != nil {
		t.Fatal(err)
	}
	app, err := New(Config{
		IP: "127.0.0.1", Port: 8080, Password: "metadata-password", StateDir: filepath.Join(base, "state"),
		AllowedRoots: []AllowedRoot{{Path: share}}, Shares: []Share{{ID: "files", Name: "Files", Path: share}},
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	return app, share
}

func metadataList(t *testing.T, app *Server) map[string]Entry {
	t.Helper()
	response := httptest.NewRecorder()
	if err := app.list(response, httptest.NewRequest("GET", "/api/files?share=files", nil)); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]Entry)
	for _, raw := range body.Entries {
		assertMetadataJSON(t, raw)
		var entry Entry
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatal(err)
		}
		result[entry.Name] = entry
	}
	return result
}

func metadataTrash(t *testing.T, app *Server) []TrashEntry {
	t.Helper()
	response := httptest.NewRecorder()
	if err := app.listTrash(response, httptest.NewRequest("GET", "/api/trash?share=files", nil)); err != nil {
		t.Fatal(err)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	result := make([]TrashEntry, len(raw))
	for i, entry := range raw {
		assertMetadataJSON(t, entry)
		if err := json.Unmarshal(entry, &result[i]); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func assertMetadataJSON(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"extension", "permissions", "owner", "group", "created", "isSymlink"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing %s: %s", key, raw)
		}
	}
	for _, key := range []string{"extension", "permissions", "owner", "group"} {
		var value string
		if err := json.Unmarshal(fields[key], &value); err != nil {
			t.Errorf("%s is not a string: %s", key, raw)
		}
	}
	if string(fields["created"]) != "null" {
		var value string
		if err := json.Unmarshal(fields["created"], &value); err != nil {
			t.Fatal(err)
		}
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			t.Errorf("created is not RFC3339: %q", value)
		}
	}
}

func assertMetadataMatches(t *testing.T, absolute string, entry Entry) {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Lstat(absolute, &stat); err != nil {
		t.Fatal(err)
	}
	owner := strconv.FormatUint(uint64(stat.Uid), 10)
	if account, err := user.LookupId(owner); err == nil && account.Username != "" {
		owner = account.Username
	}
	group := strconv.FormatUint(uint64(stat.Gid), 10)
	if account, err := user.LookupGroupId(group); err == nil && account.Name != "" {
		group = account.Name
	}
	if entry.Permissions != fmt.Sprintf("%04o", stat.Mode&07777) || entry.Owner != owner || entry.Group != group {
		t.Errorf("%s identity/mode = %+v, want %04o %s:%s", absolute, entry, stat.Mode&07777, owner, group)
	}
	if entry.Size != stat.Size || entry.IsDir != (stat.Mode&unix.S_IFMT == unix.S_IFDIR) || entry.IsSymlink != (stat.Mode&unix.S_IFMT == unix.S_IFLNK) {
		t.Errorf("%s object metadata does not match lstat: %+v", absolute, entry)
	}
	if !entry.Modified.Equal(time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)) {
		t.Errorf("%s mtime = %s, want %+v", absolute, entry.Modified, stat.Mtim)
	}
	var extended unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, absolute, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BASIC_STATS|unix.STATX_BTIME, &extended); err == nil && extended.Mask&unix.STATX_BTIME != 0 {
		want := time.Unix(extended.Btime.Sec, int64(extended.Btime.Nsec))
		if entry.Created == nil || !entry.Created.Equal(want) {
			t.Errorf("%s birth time = %v, want %s", absolute, entry.Created, want)
		}
	} else if entry.Created != nil {
		t.Errorf("%s has unsupported birth time %s", absolute, entry.Created)
	}
}

func TestFileMetadataListing(t *testing.T) {
	app, share := newMetadataServer(t)
	for _, name := range []string{"archive.tar.GZ", "README", ".profile", ".config.JSON", "trailing.TXT."} {
		if err := os.WriteFile(filepath.Join(share, name), []byte("contents"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(share, "archive.tar.GZ"), 0754|os.ModeSetuid|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(946684800, 123456789)
	if err := os.Chtimes(filepath.Join(share, "archive.tar.GZ"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(share, "folder.ext"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(share, "folder.ext"), 0750|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("outside secret with different metadata"), 0000); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"escape.LNK": secret, "outside-dir": outside} {
		if err := os.Symlink(target, filepath.Join(share, name)); err != nil {
			t.Fatal(err)
		}
	}
	entries := metadataList(t, app)
	wantExtensions := map[string]string{
		"archive.tar.GZ": "GZ", "README": "", ".profile": "", ".config.JSON": "JSON", "trailing.TXT.": "TXT",
		"folder.ext": "", "escape.LNK": "LNK", "outside-dir": "",
	}
	if len(entries) != len(wantExtensions) {
		t.Fatalf("entries = %+v", entries)
	}
	for name, extension := range wantExtensions {
		entry, ok := entries[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if entry.Path != name || entry.Extension != extension {
			t.Errorf("%s path/extension = %+v", name, entry)
		}
		assertMetadataMatches(t, filepath.Join(share, name), entry)
	}
	if entry := entries["archive.tar.GZ"]; entry.Created != nil && entry.Created.Equal(old) {
		t.Fatal("created substituted the deliberately old mtime")
	}
	response := httptest.NewRecorder()
	if err := app.list(response, httptest.NewRequest("GET", "/api/files?share=files&path=outside-dir", nil)); err == nil {
		t.Fatal("listed through a symlink directory")
	}
}

func TestMetadataMissingBirthAndAccountCache(t *testing.T) {
	accounts := newListingAccounts()
	accounts.users[42] = "cached-user"
	accounts.groups[43] = "cached-group"
	stat := unix.Statx_t{Mask: unix.STATX_BASIC_STATS, Mode: unix.S_IFREG | 04755, Uid: 42, Gid: 43}
	stat.Mtime.Sec = 100
	stat.Ctime.Sec = 200
	stat.Btime.Sec = 300 // Without STATX_BTIME this value is not valid.
	entry := statxEntry("file.txt", &stat, accounts)
	if entry.Created != nil || entry.Permissions != "4755" || entry.Owner != "cached-user" || entry.Group != "cached-group" {
		t.Fatalf("metadata without supported birth time = %+v", entry)
	}
	stat.Mask |= unix.STATX_BTIME
	stat.Btime.Sec = 0 // The Unix epoch is valid when the birth-time mask is set.
	entry = statxEntry("file.txt", &stat, accounts)
	if entry.Created == nil || !entry.Created.Equal(time.Unix(0, 0)) {
		t.Fatalf("valid epoch birth time = %v", entry.Created)
	}
	for range 3 {
		accounts.owner(uint32(os.Getuid()))
		accounts.group(uint32(os.Getgid()))
	}
	if len(accounts.users) > 2 || len(accounts.groups) > 2 {
		t.Fatalf("account cache grew for repeated IDs: %+v", accounts)
	}
}

func TestTrashMetadataUsesRetainedObjectAndOriginalExtension(t *testing.T) {
	app, share := newMetadataServer(t)
	for _, name := range []string{"original.tar.ZIP", ".profile"} {
		if err := os.WriteFile(filepath.Join(share, name), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(share, "folder.ext"), 0750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside contents"), 0000); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(share, "link.LNK")); err != nil {
		t.Fatal(err)
	}
	sh, err := app.share("files", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"original.tar.ZIP", ".profile", "folder.ext", "link.LNK"} {
		if err := app.trashFile(sh, name); err != nil {
			t.Fatal(err)
		}
	}
	entries := metadataTrash(t, app)
	if len(entries) != 4 {
		t.Fatalf("trash = %+v", entries)
	}
	// The current retained object, not stale deletion-time DB values, supplies
	// metadata. Renaming into the slot must not change the original extension.
	for _, entry := range entries {
		if entry.Path == "original.tar.ZIP" {
			stored := filepath.Join(share, ".webui-trash", entry.ID, "data")
			if err := os.WriteFile(stored, []byte("changed retained contents"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(stored, 0641); err != nil {
				t.Fatal(err)
			}
		}
	}
	wantExtensions := map[string]string{"original.tar.ZIP": "ZIP", ".profile": "", "folder.ext": "", "link.LNK": "LNK"}
	for _, entry := range metadataTrash(t, app) {
		if entry.Extension != wantExtensions[entry.Path] {
			t.Errorf("original extension lost: %+v", entry)
		}
		stored := filepath.Join(share, ".webui-trash", entry.ID, "data")
		info, err := os.Lstat(stored)
		if err != nil {
			t.Fatal(err)
		}
		assertMetadataMatches(t, stored, Entry{Size: entry.Size, IsDir: entry.IsDir, IsSymlink: entry.IsSymlink, Modified: info.ModTime(), Permissions: entry.Permissions, Owner: entry.Owner, Group: entry.Group, Created: entry.Created})
	}
}

func TestMetadataUnknownAccountsUseNumericIDs(t *testing.T) {
	accounts := newListingAccounts()
	const id uint32 = 4294967295
	const decimal = "4294967295"
	if _, err := user.LookupId(decimal); err != nil {
		if actual := accounts.owner(id); actual != decimal {
			t.Errorf("unknown owner = %q, want %q", actual, decimal)
		}
		if accounts.users[id] != decimal {
			t.Error("unsuccessful owner lookup was not cached")
		}
	}
	if _, err := user.LookupGroupId(decimal); err != nil {
		if actual := accounts.group(id); actual != decimal {
			t.Errorf("unknown group = %q, want %q", actual, decimal)
		}
		if accounts.groups[id] != decimal {
			t.Error("unsuccessful group lookup was not cached")
		}
	}
}
