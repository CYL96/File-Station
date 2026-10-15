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
	"testing"
	"time"
)

func TestCancelledQueuedMoveLeavesSource(t *testing.T) {
	base := t.TempDir()
	files := filepath.Join(base, "files")
	if err := os.MkdirAll(filepath.Join(files, "target"), 0750); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(files, "source.txt"), []byte("do not move"), 0640)
	password := filepath.Join(base, "password")
	_ = os.WriteFile(password, []byte("correct-password-123"), 0600)
	app, err := New(Config{IP: "127.0.0.1", Port: 8080, StateDir: filepath.Join(base, "state"), PasswordFile: password, Shares: []Share{{ID: "files", Name: "Files", Path: files}}}, base)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	srv := httptest.NewServer(app)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := ""
	post := func(route string, body any) map[string]string {
		t.Helper()
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+route, bytes.NewReader(data))
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", route, resp.StatusCode, raw)
		}
		var value map[string]string
		_ = json.Unmarshal(raw, &value)
		return value
	}
	csrf = post("/api/login", map[string]string{"password": "correct-password-123"})["csrf"]
	app.mutation.Lock()
	locked := true
	defer func() {
		if locked {
			app.mutation.Unlock()
		}
	}()
	task := post("/api/operation", map[string]any{"kind": "move", "share": "files", "paths": []string{"source.txt"}, "destination": "target"})["id"]
	post("/api/tasks/cancel", map[string]string{"id": task})
	app.mutation.Unlock()
	locked = false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := app.allTasks()
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) == 1 && tasks[0].Status != "running" {
			if tasks[0].Status != "cancelled" || tasks[0].Completed != 0 {
				t.Fatalf("unexpected transition %+v", tasks[0])
			}
			source, err := os.ReadFile(filepath.Join(files, "source.txt"))
			if err != nil || string(source) != "do not move" {
				t.Fatal("queued cancellation moved source")
			}
			if _, err = os.Stat(filepath.Join(files, "target", "source.txt")); !os.IsNotExist(err) {
				t.Fatal("queued cancellation created target")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cancelled task did not settle")
}
