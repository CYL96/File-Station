package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type Task struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Status           string `json:"status"`
	Completed        int    `json:"completed"`
	Total            int    `json:"total"`
	Error            string `json:"error,omitempty"`
	CanRetry         bool   `json:"canRetry"`
	cancel           context.CancelFunc
	sourceShare      string
	destinationShare string
}
type operationBody struct {
	Kind             string   `json:"kind"`
	Share            string   `json:"share"`
	Paths            []string `json:"paths"`
	DestinationShare string   `json:"destinationShare"`
	Destination      string   `json:"destination"`
	Conflict         string   `json:"conflict"`
}

func (s *Server) saveTask(t *Task) error {
	_, err := s.db.Exec("INSERT INTO tasks(id,kind,status,completed,total,error) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,completed=excluded.completed,error=excluded.error", t.ID, t.Kind, t.Status, t.Completed, t.Total, t.Error)
	return err
}
func (s *Server) operation(w http.ResponseWriter, r *http.Request) error {
	var b operationBody
	if err := decode(w, r, &b); err != nil {
		return err
	}
	id, err := s.startOperation(b)
	if err != nil {
		return err
	}
	jsonResponse(w, map[string]string{"id": id})
	return nil
}

func (s *Server) retryTask(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		ID       string  `json:"id"`
		Conflict *string `json:"conflict"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	var status string
	var completed int
	var body sql.NullString
	err := s.db.QueryRow("SELECT tasks.status,tasks.completed,task_operations.body FROM tasks LEFT JOIN task_operations ON task_operations.id=tasks.id WHERE tasks.id=?", request.ID).Scan(&status, &completed, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(404, "任务不存在")
	}
	if err != nil {
		return err
	}
	if status != "error" || !body.Valid {
		return fail(409, "此任务不能重试")
	}
	var b operationBody
	if err = json.Unmarshal([]byte(body.String), &b); err != nil {
		return err
	}
	if completed < 0 || completed >= len(b.Paths) {
		return fail(409, "此任务没有可重试的项目")
	}
	b.Paths = b.Paths[completed:]
	if request.Conflict != nil {
		b.Conflict = *request.Conflict
	}
	id, err := s.startOperation(b)
	if err != nil {
		return err
	}
	jsonResponse(w, map[string]string{"id": id})
	return nil
}

func (s *Server) startOperation(b operationBody) (string, error) {
	if b.Kind != "copy" && b.Kind != "move" && b.Kind != "trash" {
		return "", fail(400, "操作无效")
	}
	if len(b.Paths) == 0 || len(b.Paths) > 1000 {
		return "", fail(400, "请选择 1 到 1000 项")
	}
	if err := conflictPolicy(b.Conflict); err != nil {
		return "", err
	}
	src, err := s.share(b.Share, b.Kind != "copy")
	if err != nil {
		return "", err
	}
	var dst *Share
	destination := "."
	if b.Kind != "trash" {
		if b.DestinationShare == "" {
			b.DestinationShare = b.Share
		}
		dst, err = s.share(b.DestinationShare, true)
		if err != nil {
			return "", err
		}
		destination, err = safePath(b.Destination)
		if err != nil {
			return "", err
		}
		if err = noLinks(dst.root, destination); err != nil {
			return "", err
		}
		info, err := dst.root.Stat(destination)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fail(400, "目标不是文件夹")
		}
	}
	seen := map[string]bool{}
	for i, p := range b.Paths {
		p, err = safePath(p)
		if err != nil {
			return "", err
		}
		if p == "." {
			return "", fail(403, "不能操作共享根目录")
		}
		if seen[p] {
			return "", fail(400, "不能重复选择文件")
		}
		seen[p] = true
		b.Paths[i] = p
		if err = noLinks(src.root, path.Dir(p)); err != nil {
			return "", err
		}
		if _, err = src.root.Lstat(p); err != nil {
			return "", err
		}
		if dst == src {
			target := path.Join(destination, path.Base(p))
			if target == p || strings.HasPrefix(destination, p+"/") || destination == p {
				return "", fail(400, "不能复制或移动到自身或子目录")
			}
		}
	}
	for p := range seen {
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return "", fail(400, "不能同时选择父目录和子项")
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &Task{ID: token(), Kind: b.Kind, Status: "running", Total: len(b.Paths), cancel: cancel, sourceShare: b.Share, destinationShare: b.DestinationShare}
	body, err := json.Marshal(b)
	if err != nil {
		cancel()
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		cancel()
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO tasks(id,kind,status,completed,total,error) VALUES(?,?,?,?,?,?)", t.ID, t.Kind, t.Status, t.Completed, t.Total, t.Error); err == nil {
		_, err = tx.Exec("INSERT INTO task_operations(id,body) VALUES(?,?)", t.ID, string(body))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		cancel()
		return "", err
	}
	s.tasksMu.Lock()
	s.tasks[t.ID] = t
	s.tasksMu.Unlock()
	// Slice edits must not change the descriptors captured by a background task.
	source := *src
	src = &source
	if dst != nil {
		destinationShare := *dst
		dst = &destinationShare
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer cancel()
		var workErr error
		for _, p := range b.Paths {
			if workErr = ctx.Err(); workErr != nil {
				break
			}
			s.mutation.Lock()
			if workErr = ctx.Err(); workErr != nil {
				s.mutation.Unlock()
				break
			}
			if b.Kind == "trash" {
				workErr = s.trashFile(src, p)
			} else {
				var skip bool
				var target string
				target, skip, workErr = available(dst.root, path.Join(destination, path.Base(p)), b.Conflict)
				if workErr == nil && !skip {
					if b.Kind == "move" {
						workErr = renameBetween(src.root, p, dst.root, target, b.Conflict == "replace")
						if errors.Is(workErr, unix.EXDEV) {
							workErr = s.publishCopy(ctx, src.root, p, dst.root, target, b.Conflict)
							if workErr == nil {
								workErr = src.root.RemoveAll(p)
							}
						}
					} else {
						workErr = s.publishCopy(ctx, src.root, p, dst.root, target, b.Conflict)
					}
				}
			}
			s.mutation.Unlock()
			if workErr != nil {
				break
			}
			s.tasksMu.Lock()
			t.Completed++
			workErr = s.saveTask(t)
			s.tasksMu.Unlock()
			if workErr != nil {
				break
			}
		}
		s.tasksMu.Lock()
		if workErr == nil {
			t.Status = "done"
		} else if errors.Is(workErr, context.Canceled) {
			t.Status = "cancelled"
			t.Error = "已取消；此前完成的文件操作不会撤销"
		} else {
			t.Status = "error"
			t.Error = workErr.Error()
		}
		_ = s.saveTask(t)
		delete(s.tasks, t.ID)
		s.tasksMu.Unlock()
	}()
	return t.ID, nil
}
func (s *Server) allTasks() ([]Task, error) {
	rows, err := s.db.Query("SELECT tasks.id,tasks.kind,tasks.status,tasks.completed,tasks.total,tasks.error,(tasks.status='error' AND tasks.completed>=0 AND tasks.completed<tasks.total AND task_operations.id IS NOT NULL) FROM tasks LEFT JOIN task_operations ON task_operations.id=tasks.id ORDER BY tasks.rowid DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Task{}
	for rows.Next() {
		var t Task
		if err = rows.Scan(&t.ID, &t.Kind, &t.Status, &t.Completed, &t.Total, &t.Error, &t.CanRetry); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}
func (s *Server) getTasks(w http.ResponseWriter, r *http.Request) error {
	tasks, err := s.allTasks()
	if err != nil {
		return err
	}
	jsonResponse(w, tasks)
	return nil
}
func (s *Server) clearTasks(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		IDs []string `json:"ids"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	if len(b.IDs) > 0 {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		stmt, err := tx.Prepare("DELETE FROM tasks WHERE id=? AND status='done'")
		if err != nil {
			return err
		}
		defer stmt.Close()
		cleanup, err := tx.Prepare("DELETE FROM task_operations WHERE id IN (SELECT id FROM tasks WHERE id=? AND status='done')")
		if err != nil {
			return err
		}
		defer cleanup.Close()
		for _, id := range b.IDs {
			if _, err = cleanup.Exec(id); err != nil {
				return err
			}
			if _, err = stmt.Exec(id); err != nil {
				return err
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	jsonResponse(w, map[string]bool{"ok": true})
	return nil
}
func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		ID string `json:"id"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	t := s.tasks[b.ID]
	if t == nil || t.Status != "running" {
		return fail(404, "任务不在运行")
	}
	t.cancel()
	jsonResponse(w, map[string]bool{"ok": true})
	return nil
}
func (s *Server) CancelTasks() {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	for _, t := range s.tasks {
		if t.cancel != nil {
			t.cancel()
		}
	}
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fail(500, "事件流不可用")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		tasks, err := s.allTasks()
		if err != nil {
			return nil
		}
		data, _ := json.Marshal(tasks)
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err = fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return nil
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return nil
		case <-ticker.C:
		}
		if _, err = s.session(r); err != nil {
			return nil
		}
	}
}

type TrashEntry struct {
	ID          string     `json:"id"`
	Path        string     `json:"path"`
	DeletedAt   string     `json:"deletedAt"`
	Size        int64      `json:"size"`
	IsDir       bool       `json:"isDir"`
	IsSymlink   bool       `json:"isSymlink"`
	Extension   string     `json:"extension"`
	Permissions string     `json:"permissions"`
	Owner       string     `json:"owner"`
	Group       string     `json:"group"`
	Created     *time.Time `json:"created"`
}

func (s *Server) trashFile(sh *Share, p string) error {
	info, err := sh.root.Lstat(p)
	if err != nil {
		return err
	}
	if err = sh.root.MkdirAll(".webui-trash", 0700); err != nil {
		return err
	}
	if err = noLinks(sh.root, ".webui-trash"); err != nil {
		return err
	}
	id := token()
	slot := path.Join(".webui-trash", id)
	if err = sh.root.Mkdir(slot, 0700); err != nil {
		return err
	}
	defer sh.root.Remove(slot)
	// Metadata first: crash after rename leaves recoverable contents and metadata.
	_, err = s.db.Exec("INSERT INTO trash VALUES(?,?,?,?,?,?)", id, sh.ID, p, time.Now().UTC().Format(time.RFC3339), info.Size(), info.IsDir())
	if err != nil {
		return err
	}
	if err = renameBetween(sh.root, p, sh.root, path.Join(slot, "data"), false); err != nil {
		_, _ = s.db.Exec("DELETE FROM trash WHERE id=?", id)
		return err
	}
	return nil
}
func (s *Server) listTrash(w http.ResponseWriter, r *http.Request) error {
	sh, err := s.share(r.URL.Query().Get("share"), false)
	if err != nil {
		return err
	}
	rows, err := s.db.Query("SELECT id,path,deleted_at,size,is_dir FROM trash WHERE share=? ORDER BY deleted_at DESC", sh.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	result := []TrashEntry{}
	accounts := newListingAccounts()
	for rows.Next() {
		var e TrashEntry
		if err = rows.Scan(&e.ID, &e.Path, &e.DeletedAt, &e.Size, &e.IsDir); err != nil {
			return err
		}
		slot := path.Join(".webui-trash", e.ID)
		if err := noLinks(sh.root, slot); err != nil {
			continue
		}
		dir, err := sh.root.OpenFile(slot, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			continue
		}
		metadata, err := readEntryMetadata(dir, "data", path.Base(e.Path), accounts)
		dir.Close()
		if err != nil {
			continue
		}
		e.Size, e.IsDir, e.IsSymlink = metadata.Size, metadata.IsDir, metadata.IsSymlink
		e.Extension, e.Permissions = metadata.Extension, metadata.Permissions
		e.Owner, e.Group, e.Created = metadata.Owner, metadata.Group, metadata.Created
		result = append(result, e)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	jsonResponse(w, result)
	return nil
}

type trashBody struct {
	Share    string `json:"share"`
	ID       string `json:"id"`
	Conflict string `json:"conflict"`
}

func (s *Server) trashRequest(w http.ResponseWriter, r *http.Request) (*Share, trashBody, string, error) {
	var b trashBody
	if err := decode(w, r, &b); err != nil {
		return nil, b, "", err
	}
	sh, err := s.share(b.Share, true)
	if err != nil {
		return nil, b, "", err
	}
	if len(b.ID) != 64 || !validName(b.ID) {
		return nil, b, "", fail(400, "回收站条目无效")
	}
	var original string
	err = s.db.QueryRow("SELECT path FROM trash WHERE id=? AND share=?", b.ID, b.Share).Scan(&original)
	if err != nil {
		return nil, b, "", fail(404, "回收站条目不存在")
	}
	if err = noLinks(sh.root, path.Join(".webui-trash", b.ID)); err != nil {
		return nil, b, "", err
	}
	return sh, b, original, nil
}
func (s *Server) restoreTrash(w http.ResponseWriter, r *http.Request) (result error) {
	s.mutation.Lock()
	defer s.mutation.Unlock()
	sh, b, original, err := s.trashRequest(w, r)
	if err != nil {
		return err
	}
	parent := path.Dir(original)
	created, err := s.mkdirParents(sh.root, parent)
	skipped := false
	defer func() {
		for i := len(created) - 1; i >= 0; i-- {
			if chmodErr := sh.root.Chmod(created[i], s.dirMode()); result == nil && chmodErr != nil {
				result = chmodErr
			}
		}
		if result == nil {
			jsonResponse(w, map[string]bool{"ok": true, "skipped": skipped})
		}
	}()
	if err != nil {
		return err
	}
	if err = noLinks(sh.root, parent); err != nil {
		return err
	}
	target, skip, err := available(sh.root, original, b.Conflict)
	if err != nil {
		return err
	}
	if skip {
		skipped = true
		return nil
	}
	if err = renameBetween(sh.root, path.Join(".webui-trash", b.ID, "data"), sh.root, target, b.Conflict == "replace"); err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM trash WHERE id=?", b.ID)
	_ = sh.root.Remove(path.Join(".webui-trash", b.ID))
	if err != nil {
		return err
	}
	return nil
}
func (s *Server) deleteTrash(w http.ResponseWriter, r *http.Request) error {
	s.mutation.Lock()
	defer s.mutation.Unlock()
	sh, b, _, err := s.trashRequest(w, r)
	if err != nil {
		return err
	}
	if err = sh.root.RemoveAll(path.Join(".webui-trash", b.ID)); err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM trash WHERE id=?", b.ID)
	if err != nil {
		return err
	}
	jsonResponse(w, map[string]bool{"ok": true})
	return nil
}
