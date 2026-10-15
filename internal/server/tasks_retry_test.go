package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type taskRetryFixture struct {
	t      *testing.T
	app    *Server
	config Config
	base   string
	files  string
	cookie *http.Cookie
	csrf   string
}

func newTaskRetryFixture(t *testing.T) *taskRetryFixture {
	t.Helper()
	base := t.TempDir()
	files := filepath.Join(base, "files")
	if err := os.MkdirAll(filepath.Join(files, "target"), 0750); err != nil {
		t.Fatal(err)
	}
	f := &taskRetryFixture{t: t, base: base, files: files, config: Config{IP: "127.0.0.1", Port: 8080, StateDir: filepath.Join(base, "state"), Password: "correct-password-123", AllowedRoots: []AllowedRoot{{Path: files}}, Shares: []Share{{ID: "files", Name: "Files", Path: files}}}}
	f.open()
	t.Cleanup(func() { f.app.Close() })
	return f
}

func (f *taskRetryFixture) open() {
	f.t.Helper()
	var err error
	f.app, err = New(f.config, f.base)
	if err != nil {
		f.t.Fatal(err)
	}
	f.cookie = nil
	f.csrf = ""
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"correct-password-123"}`))
	response := httptest.NewRecorder()
	f.app.ServeHTTP(response, req)
	if response.Code != 200 {
		f.t.Fatalf("login: %d %s", response.Code, response.Body.String())
	}
	var auth map[string]string
	if err = json.Unmarshal(response.Body.Bytes(), &auth); err != nil {
		f.t.Fatal(err)
	}
	f.csrf = auth["csrf"]
	f.cookie = response.Result().Cookies()[0]
}

func (f *taskRetryFixture) request(method, route string, body any, want int) []byte {
	f.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	req := httptest.NewRequest(method, route, bytes.NewReader(data))
	if f.cookie != nil {
		req.AddCookie(f.cookie)
	}
	req.Header.Set("X-CSRF-Token", f.csrf)
	response := httptest.NewRecorder()
	f.app.ServeHTTP(response, req)
	if response.Code != want {
		f.t.Fatalf("%s %s: got %d wanted %d: %s", method, route, response.Code, want, response.Body.String())
	}
	return response.Body.Bytes()
}

func (f *taskRetryFixture) start(route string, body any) string {
	f.t.Helper()
	var response map[string]string
	if err := json.Unmarshal(f.request("POST", route, body, 200), &response); err != nil {
		f.t.Fatal(err)
	}
	if response["id"] == "" {
		f.t.Fatal("missing task ID")
	}
	return response["id"]
}

func (f *taskRetryFixture) task(id string) Task {
	f.t.Helper()
	raw := f.request("GET", "/api/tasks", nil, 200)
	if strings.Contains(string(raw), `"paths"`) || strings.Contains(string(raw), `"destination"`) || strings.Contains(string(raw), `"share"`) {
		f.t.Fatalf("task response leaked operation parameters: %s", raw)
	}
	var tasks []Task
	if err := json.Unmarshal(raw, &tasks); err != nil {
		f.t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	f.t.Fatalf("task %s missing: %s", id, raw)
	return Task{}
}

func (f *taskRetryFixture) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.files, name), []byte(content), 0640); err != nil {
		f.t.Fatal(err)
	}
}

func TestRetryTasksPersistsAndSkipsCompletedPrefix(t *testing.T) {
	for _, kind := range []string{"copy", "move", "trash"} {
		t.Run(kind, func(t *testing.T) {
			f := newTaskRetryFixture(t)
			f.write("first.txt", "first original")
			f.write("second.txt", "second original")
			if kind == "trash" {
				if _, err := f.app.db.Exec("CREATE TRIGGER reject_second_trash BEFORE INSERT ON trash WHEN NEW.path='second.txt' BEGIN SELECT RAISE(ABORT, 'trash failure'); END"); err != nil {
					t.Fatal(err)
				}
			} else {
				f.write("target/second.txt", "collision")
			}
			id := f.start("/api/operation", operationBody{Kind: kind, Share: "files", Paths: []string{"first.txt", "second.txt"}, Destination: "target", Conflict: "error"})
			f.app.workers.Wait()
			failed := f.task(id)
			if failed.Status != "error" || failed.Completed != 1 || failed.Total != 2 || !failed.CanRetry {
				t.Fatalf("failed task: %+v", failed)
			}
			// A page refresh and a fresh server must both derive retryability from persisted state.
			f.app.Close()
			f.open()
			if got := f.task(id); !reflect.DeepEqual(got, failed) {
				t.Fatalf("restart changed failed task: %+v, wanted %+v", got, failed)
			}
			if kind == "trash" {
				if _, err := f.app.db.Exec("DROP TRIGGER reject_second_trash"); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(filepath.Join(f.files, "target", "second.txt")); err != nil {
				t.Fatal(err)
			}
			if kind == "copy" {
				f.write("first.txt", "changed source must not be copied again")
			} else if _, err := os.Stat(filepath.Join(f.files, "first.txt")); !os.IsNotExist(err) {
				t.Fatalf("successful prefix still exists at source: %v", err)
			}
			retried := f.start("/api/tasks/retry", map[string]string{"id": id})
			f.app.workers.Wait()
			if retried == id {
				t.Fatal("retry replaced failed history")
			}
			if got := f.task(retried); got.Status != "done" || got.Completed != 1 || got.Total != 1 || got.CanRetry {
				t.Fatalf("retried task: %+v", got)
			}
			if got := f.task(id); !reflect.DeepEqual(got, failed) {
				t.Fatalf("retry changed failed history: %+v", got)
			}
			if kind == "trash" {
				var count int
				if err := f.app.db.QueryRow("SELECT count(*) FROM trash").Scan(&count); err != nil || count != 2 {
					t.Fatalf("trash count: %d, %v", count, err)
				}
			} else {
				for _, name := range []string{"first", "second"} {
					data, err := os.ReadFile(filepath.Join(f.files, "target", name+".txt"))
					if err != nil || string(data) != name+" original" {
						t.Fatalf("%s target: %q, %v", name, data, err)
					}
				}
			}
			f.request("POST", "/api/tasks/clear", map[string]any{"ids": []string{id, retried}}, 200)
			var retained, removed int
			if err := f.app.db.QueryRow("SELECT count(*) FROM task_operations WHERE id=?", id).Scan(&retained); err != nil {
				t.Fatal(err)
			}
			if err := f.app.db.QueryRow("SELECT count(*) FROM task_operations WHERE id=?", retried).Scan(&removed); err != nil {
				t.Fatal(err)
			}
			if retained != 1 || removed != 0 {
				t.Fatalf("clear retry parameters: failed=%d successful=%d", retained, removed)
			}
		})
	}
}

func TestRetryTaskRejectsInvalidStatesAndRequests(t *testing.T) {
	f := newTaskRetryFixture(t)
	f.write("source.txt", "source")
	body, err := json.Marshal(operationBody{Kind: "copy", Share: "files", Paths: []string{"source.txt"}, Destination: "target"})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"running", "done", "cancelled", "error"} {
		if err = f.app.saveTask(&Task{ID: status, Kind: "copy", Status: status, Total: 1}); err != nil {
			t.Fatal(err)
		}
		if status != "error" {
			if _, err = f.app.db.Exec("INSERT INTO task_operations(id,body) VALUES(?,?)", status, string(body)); err != nil {
				t.Fatal(err)
			}
		}
		if f.task(status).CanRetry {
			t.Fatalf("%s unexpectedly retryable", status)
		}
		f.request("POST", "/api/tasks/retry", map[string]string{"id": status}, 409)
	}
	if err = f.app.saveTask(&Task{ID: "exhausted", Kind: "copy", Status: "error", Completed: 1, Total: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.db.Exec("INSERT INTO task_operations(id,body) VALUES(?,?)", "exhausted", string(body)); err != nil {
		t.Fatal(err)
	}
	if f.task("exhausted").CanRetry {
		t.Fatal("exhausted task unexpectedly retryable")
	}
	f.request("POST", "/api/tasks/retry", map[string]string{"id": "exhausted"}, 409)
	f.request("POST", "/api/tasks/retry", map[string]string{"id": "missing"}, 404)
	f.request("POST", "/api/tasks/retry", map[string]any{"id": 123}, 400)
	f.request("POST", "/api/tasks/retry", map[string]any{"id": "error", "paths": []string{"source.txt"}}, 400)
	csrf := f.csrf
	f.csrf = ""
	f.request("POST", "/api/tasks/retry", map[string]string{"id": "error"}, 403)
	f.csrf = csrf
	cookie := f.cookie
	f.cookie = nil
	f.request("POST", "/api/tasks/retry", map[string]string{"id": "error"}, 401)
	f.cookie = cookie
}

func TestRetryTaskConflictPolicyAndShareLocks(t *testing.T) {
	f := newTaskRetryFixture(t)
	f.write("source.txt", "source")
	f.write("target/source.txt", "collision")
	id := f.start("/api/operation", operationBody{Kind: "copy", Share: "files", Paths: []string{"source.txt"}, Destination: "target", Conflict: "error"})
	f.app.workers.Wait()
	if got := f.task(id); got.Status != "error" || !got.CanRetry {
		t.Fatalf("expected retryable conflict: %+v", got)
	}
	f.request("POST", "/api/tasks/retry", map[string]string{"id": id, "conflict": "invalid"}, 400)
	stillConflicting := f.start("/api/tasks/retry", map[string]string{"id": id})
	f.app.workers.Wait()
	if got := f.task(stillConflicting); got.Status != "error" || got.Completed != 0 || !got.CanRetry {
		t.Fatalf("omitted policy did not retain conflict: %+v", got)
	}
	f.request("POST", "/api/settings/shares/save", map[string]any{"id": "files", "name": "Files", "path": f.files, "readOnly": true}, 200)
	f.request("POST", "/api/tasks/retry", map[string]string{"id": id, "conflict": "replace"}, 403)
	f.request("POST", "/api/settings/shares/save", map[string]any{"id": "files", "name": "Files", "path": f.files, "readOnly": false}, 200)
	f.app.mutation.Lock()
	locked := true
	defer func() {
		if locked {
			f.app.mutation.Unlock()
		}
	}()
	retried := f.start("/api/tasks/retry", map[string]string{"id": id, "conflict": "replace"})
	f.request("POST", "/api/settings/shares/save", map[string]any{"id": "files", "name": "Files", "path": f.files, "readOnly": true}, 409)
	f.request("POST", "/api/settings/shares/remove", map[string]string{"id": "files"}, 409)
	f.app.mutation.Unlock()
	locked = false
	f.app.workers.Wait()
	if got := f.task(retried); got.Status != "done" || got.Completed != 1 {
		t.Fatalf("retry replace: %+v", got)
	}
	if data, err := os.ReadFile(filepath.Join(f.files, "target", "source.txt")); err != nil || string(data) != "source" {
		t.Fatalf("replace target: %q %v", data, err)
	}
}

func TestRetryTaskRecoversInterruptedTaskAfterRestart(t *testing.T) {
	f := newTaskRetryFixture(t)
	f.write("remaining.txt", "remaining")
	body, err := json.Marshal(operationBody{Kind: "move", Share: "files", Paths: []string{"already-moved.txt", "remaining.txt"}, Destination: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.app.saveTask(&Task{ID: "interrupted", Kind: "move", Status: "running", Completed: 1, Total: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.db.Exec("INSERT INTO task_operations(id,body) VALUES(?,?)", "interrupted", string(body)); err != nil {
		t.Fatal(err)
	}
	f.app.Close()
	f.open()
	if got := f.task("interrupted"); got.Status != "error" || got.Completed != 1 || !got.CanRetry {
		t.Fatalf("interrupted task after restart: %+v", got)
	}
	id := f.start("/api/tasks/retry", map[string]string{"id": "interrupted"})
	f.app.workers.Wait()
	if got := f.task(id); got.Status != "done" || got.Total != 1 || got.Completed != 1 {
		t.Fatalf("interrupted task retry: %+v", got)
	}
}

func TestRetryTaskRevalidatesChangedFilesystem(t *testing.T) {
	for _, change := range []string{"source-boundary", "destination-boundary", "missing-source", "move-readonly", "trash-readonly"} {
		t.Run(change, func(t *testing.T) {
			f := newTaskRetryFixture(t)
			if err := os.Mkdir(filepath.Join(f.files, "source"), 0750); err != nil {
				t.Fatal(err)
			}
			f.write("source/file.txt", "source")
			kind := "move"
			if change == "trash-readonly" {
				kind = "trash"
				if _, err := f.app.db.Exec("CREATE TRIGGER reject_trash BEFORE INSERT ON trash BEGIN SELECT RAISE(ABORT, 'trash failure'); END"); err != nil {
					t.Fatal(err)
				}
			} else {
				f.write("target/file.txt", "collision")
			}
			id := f.start("/api/operation", operationBody{Kind: kind, Share: "files", Paths: []string{"source/file.txt"}, Destination: "target"})
			f.app.workers.Wait()
			if got := f.task(id); got.Status != "error" || !got.CanRetry {
				t.Fatalf("expected retryable task: %+v", got)
			}
			want := 403
			switch change {
			case "source-boundary", "destination-boundary":
				outside := filepath.Join(f.base, "outside")
				if err := os.Mkdir(outside, 0750); err != nil {
					t.Fatal(err)
				}
				dir := "source"
				if change == "destination-boundary" {
					dir = "target"
				}
				if err := os.Rename(filepath.Join(f.files, dir), filepath.Join(f.files, dir+"-original")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(f.files, dir)); err != nil {
					t.Fatal(err)
				}
			case "missing-source":
				if err := os.Remove(filepath.Join(f.files, "source", "file.txt")); err != nil {
					t.Fatal(err)
				}
				want = 404
			default:
				f.request("POST", "/api/settings/shares/save", map[string]any{"id": "files", "name": "Files", "path": f.files, "readOnly": true}, 200)
			}
			f.request("POST", "/api/tasks/retry", map[string]string{"id": id, "conflict": "replace"}, want)
			var count int
			if err := f.app.db.QueryRow("SELECT count(*) FROM tasks").Scan(&count); err != nil || count != 1 {
				t.Fatalf("rejected retry created task: count=%d err=%v", count, err)
			}
		})
	}
}

func TestOperationPersistsRetryParametersAtomically(t *testing.T) {
	f := newTaskRetryFixture(t)
	f.write("source.txt", "source")
	if _, err := f.app.db.Exec("CREATE TRIGGER reject_operation BEFORE INSERT ON task_operations BEGIN SELECT RAISE(ABORT, 'persistence failure'); END"); err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/operation", operationBody{Kind: "move", Share: "files", Paths: []string{"source.txt"}, Destination: "target"}, 400)
	var count int
	if err := f.app.db.QueryRow("SELECT (SELECT count(*) FROM tasks)+(SELECT count(*) FROM task_operations)").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial task persisted: count=%d err=%v", count, err)
	}
	if data, err := os.ReadFile(filepath.Join(f.files, "source.txt")); err != nil || string(data) != "source" {
		t.Fatalf("failed persistence moved source: %q %v", data, err)
	}
}
