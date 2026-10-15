package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileLifecycleAndBoundaries(t *testing.T) {
	base := t.TempDir()
	share := filepath.Join(base, "files")
	other := filepath.Join(base, "other")
	readOnly := filepath.Join(base, "readonly")
	for _, p := range []string{share, other, readOnly} {
		if err := os.Mkdir(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	password := filepath.Join(base, "password")
	if err := os.WriteFile(password, []byte("correct-password-123"), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := New(Config{IP: "127.0.0.1", Port: 8080, StateDir: filepath.Join(base, "state"), PasswordFile: password, AllowedRoots: []AllowedRoot{{Path: share}, {Path: other}, {Path: readOnly, ReadOnly: true}}, Shares: []Share{{ID: "files", Name: "Files", Path: share}, {ID: "other", Name: "Other", Path: other}, {ID: "readonly", Name: "Read only", Path: readOnly, ReadOnly: true}}}, base)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	httpServer := httptest.NewServer(app)
	defer httpServer.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := ""
	request := func(method, p string, body any, want int) []byte {
		t.Helper()
		var data []byte
		switch b := body.(type) {
		case string:
			data = []byte(b)
		case nil:
		default:
			data, _ = json.Marshal(b)
		}
		req, _ := http.NewRequest(method, httpServer.URL+p, bytes.NewReader(data))
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		result, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: got %d wanted %d: %s", method, p, resp.StatusCode, want, result)
		}
		return result
	}
	request("GET", "/api/shares", nil, 401)
	session := request("POST", "/api/login", map[string]string{"password": "correct-password-123"}, 200)
	var auth map[string]string
	if err = json.Unmarshal(session, &auth); err != nil {
		t.Fatal(err)
	}
	request("POST", "/api/mkdir", map[string]string{"share": "files", "name": "blocked"}, 403)
	csrf = auth["csrf"]
	request("POST", "/api/mkdir", map[string]string{"share": "readonly", "name": "blocked"}, 403)
	request("POST", "/api/mkdir", map[string]string{"share": "files", "name": "projects"}, 200)
	request("POST", "/api/upload?share=files&path=projects&name=note.txt", "hello real filesystem", 200)
	request("POST", "/api/upload?share=files&path=projects&name=note.txt", "silent overwrite", 409)
	actual, err := os.ReadFile(filepath.Join(share, "projects", "note.txt"))
	if err != nil || string(actual) != "hello real filesystem" {
		t.Fatalf("collision changed file: %s %v", actual, err)
	}
	request("POST", "/api/upload?share=files&path=projects&name=note.txt&conflict=keep", "second copy", 200)
	request("GET", "/api/files?share=files&path=../", nil, 403)
	secret := filepath.Join(base, "outside.txt")
	_ = os.WriteFile(secret, []byte("secret"), 0600)
	if err = os.Symlink(secret, filepath.Join(share, "escape")); err != nil {
		t.Fatal(err)
	}
	request("GET", "/api/download?share=files&path=escape", nil, 403)
	request("GET", "/api/files?share=files&path=.webui-trash", nil, 403)
	if err = os.WriteFile(filepath.Join(share, "external.txt"), []byte("host edit"), 0640); err != nil {
		t.Fatal(err)
	}
	listing := request("GET", "/api/files?share=files", nil, 200)
	if !strings.Contains(string(listing), "external.txt") {
		t.Fatal("host filesystem changes missing")
	}
	request("POST", "/api/operation", map[string]any{"kind": "copy", "share": "files", "paths": []string{"projects"}, "destination": "projects"}, 400)
	operation := func(body map[string]any, want string) {
		t.Helper()
		raw := request("POST", "/api/operation", body, 200)
		var started map[string]string
		_ = json.Unmarshal(raw, &started)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var tasks []Task
			_ = json.Unmarshal(request("GET", "/api/tasks", nil, 200), &tasks)
			for _, task := range tasks {
				if task.ID == started["id"] && task.Status != "running" {
					if task.Status != want {
						t.Fatalf("task: %+v", task)
					}
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("task did not finish")
	}
	operation(map[string]any{"kind": "copy", "share": "files", "paths": []string{"projects"}, "destinationShare": "other"}, "done")
	copied, err := os.ReadFile(filepath.Join(other, "projects", "note.txt"))
	if err != nil || string(copied) != string(actual) {
		t.Fatalf("recursive copy lost content: %s %v", copied, err)
	}
	operation(map[string]any{"kind": "copy", "share": "files", "paths": []string{"projects"}, "destinationShare": "other"}, "error")
	operation(map[string]any{"kind": "move", "share": "files", "paths": []string{"external.txt"}, "destinationShare": "other"}, "done")
	if _, err = os.Stat(filepath.Join(share, "external.txt")); !os.IsNotExist(err) {
		t.Fatal("move left source")
	}
	moved, _ := os.ReadFile(filepath.Join(other, "external.txt"))
	if string(moved) != "host edit" {
		t.Fatal("move changed content")
	}
	operation(map[string]any{"kind": "trash", "share": "files", "paths": []string{"projects"}}, "done")
	if _, err = os.Stat(filepath.Join(share, "projects")); !os.IsNotExist(err) {
		t.Fatal("trash left original")
	}
	var trash []TrashEntry
	_ = json.Unmarshal(request("GET", "/api/trash?share=files", nil, 200), &trash)
	if len(trash) != 1 || trash[0].Path != "projects" {
		t.Fatalf("trash metadata: %+v", trash)
	}
	request("POST", "/api/trash/restore", map[string]string{"share": "files", "id": trash[0].ID}, 200)
	restored, _ := os.ReadFile(filepath.Join(share, "projects", "note.txt"))
	if string(restored) != string(actual) {
		t.Fatal("restore lost content")
	}
	request("POST", "/api/rename", map[string]string{"share": "files", "path": "projects/note.txt", "name": "renamed.txt"}, 200)
	if got := request("GET", "/api/preview?share=files&path=projects/renamed.txt", nil, 200); string(got) != string(actual) {
		t.Fatal("preview changed content")
	}
	req, _ := http.NewRequest("GET", httpServer.URL+"/api/download?share=files&path=projects/renamed.txt", nil)
	req.Header.Set("Range", "bytes=0-4")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(data) != "hello" {
		t.Fatalf("range download: %d %q", resp.StatusCode, data)
	}
	operation(map[string]any{"kind": "trash", "share": "files", "paths": []string{"projects"}}, "done")
	_ = json.Unmarshal(request("GET", "/api/trash?share=files", nil, 200), &trash)
	request("POST", "/api/trash/delete", map[string]string{"share": "files", "id": trash[0].ID}, 200)
	if _, err = os.Stat(filepath.Join(share, ".webui-trash", trash[0].ID)); !os.IsNotExist(err) {
		t.Fatal("permanent deletion retained data")
	}
	request("POST", "/api/logout", nil, 200)
	request("GET", "/api/files?share=files", nil, 401)
}

func TestClearTasksOnlyDeletesRequestedSuccessfulTasks(t *testing.T) {
	base := t.TempDir()
	files := filepath.Join(base, "files")
	if err := os.Mkdir(files, 0750); err != nil {
		t.Fatal(err)
	}
	app, err := New(Config{IP: "127.0.0.1", Port: 8080, StateDir: filepath.Join(base, "state"), Password: "correct-password-123", AllowedRoots: []AllowedRoot{{Path: files}}, Shares: []Share{{ID: "files", Name: "Files", Path: files}}}, base)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	httpServer := httptest.NewServer(app)
	defer httpServer.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	csrf := ""
	request := func(method, route string, body any, want int) []byte {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, httpServer.URL+route, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: got %d wanted %d: %s", method, route, resp.StatusCode, want, raw)
		}
		return raw
	}
	assertTasks := func(want map[string]string) {
		t.Helper()
		var tasks []Task
		if err := json.Unmarshal(request("GET", "/api/tasks", nil, 200), &tasks); err != nil {
			t.Fatal(err)
		}
		if len(tasks) != len(want) {
			t.Fatalf("got tasks %+v, wanted statuses %v", tasks, want)
		}
		for _, task := range tasks {
			if status, ok := want[task.ID]; !ok || task.Status != status {
				t.Fatalf("unexpected task %+v, wanted statuses %v", task, want)
			}
		}
	}
	clear := func(ids []string) {
		t.Helper()
		var response struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(request("POST", "/api/tasks/clear", map[string]any{"ids": ids}, 200), &response); err != nil {
			t.Fatal(err)
		}
		if !response.OK {
			t.Fatal("clear did not acknowledge success")
		}
	}
	want := map[string]string{
		"selected-done": "done",
		"' OR 1=1 --":   "done",
		"other-done":    "done",
		"running":       "running",
		"error":         "error",
		"cancelled":     "cancelled",
	}
	for id, status := range want {
		if err := app.saveTask(&Task{ID: id, Kind: "copy", Status: status, Total: 1}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := []string{"selected-done", "' OR 1=1 --", "running", "error", "cancelled", "missing"}
	request("POST", "/api/tasks/clear", map[string]any{"ids": snapshot}, 401)
	var auth map[string]string
	if err := json.Unmarshal(request("POST", "/api/login", map[string]string{"password": "correct-password-123"}, 200), &auth); err != nil {
		t.Fatal(err)
	}
	request("POST", "/api/tasks/clear", map[string]any{"ids": snapshot}, 403)
	csrf = auth["csrf"]
	request("POST", "/api/tasks/clear", map[string]any{"ids": "not-an-array"}, 400)
	request("POST", "/api/tasks/clear", map[string]any{"ids": []string{}, "unexpected": true}, 400)
	assertTasks(want)
	clear([]string{})
	assertTasks(want)
	clear(snapshot)
	delete(want, "selected-done")
	delete(want, "' OR 1=1 --")
	assertTasks(want)
	assertTasks(want)
	if err := app.saveTask(&Task{ID: "finished-after-snapshot", Kind: "copy", Status: "done", Completed: 1, Total: 1}); err != nil {
		t.Fatal(err)
	}
	want["finished-after-snapshot"] = "done"
	clear(snapshot)
	assertTasks(want)
	clear([]string{})
	assertTasks(want)
}
