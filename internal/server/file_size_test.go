package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCalculateSizeRecursiveAndBoundaries(t *testing.T) {
	app, root := newMetadataServer(t)
	if err := os.MkdirAll(filepath.Join(root, "folder", "nested"), 0750); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"folder/a": "abc", "folder/nested/b": "12345", "folder/.hidden": "hi", "plain": "four", "folder/.webui-private": "not counted"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0640); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside secret"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"folder/link": outside, "folder/loop": ".", "parent-link": "folder"} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mkfifo(filepath.Join(root, "folder", "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	call := func(paths []string, trash bool) (*httptest.ResponseRecorder, error) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"share": "files", "paths": paths, "trash": trash})
		response := httptest.NewRecorder()
		err := app.calculateSize(response, httptest.NewRequest("POST", "/api/files/size", bytes.NewReader(data)))
		return response, err
	}
	response, err := call([]string{"folder", "plain"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		sizeTotals
		Items []sizeItem `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Size != 14 || result.Files != 4 || result.Folders != 2 || result.Skipped != 3 || result.Items[0].Size != 10 || result.Items[1].Size != 4 {
		t.Fatalf("wrong recursive totals: %+v", result)
	}
	for _, paths := range [][]string{{"folder", "folder/a"}, {"folder", "folder"}, {"../secret"}, {"parent-link/a"}, {".webui-private"}, {"missing"}, {".", "plain"}} {
		if _, err := call(paths, false); err == nil {
			t.Fatalf("accepted invalid paths %v", paths)
		}
	}
	response, err = call([]string{"folder/link"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Size != 0 || result.Skipped != 1 {
		t.Fatalf("followed symlink: %+v", result)
	}
	share, err := app.share("files", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.trashFile(share, "folder"); err != nil {
		t.Fatal(err)
	}
	var id string
	if err = app.db.QueryRow("SELECT id FROM trash WHERE share='files'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	response, err = call([]string{id}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Size != 10 || result.Files != 3 || result.Folders != 2 || result.Skipped != 3 {
		t.Fatalf("wrong trash totals: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = measureEntry(ctx, int(f.Fd()), &sizeTotals{}); err != context.Canceled {
		t.Fatalf("cancel ignored: %v", err)
	}
}
