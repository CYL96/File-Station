package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// AllowedRoot is a deployment-controlled boundary, never writable through settings.
type AllowedRoot struct {
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly"`
	root     *os.Root
}

func within(base, p string) bool {
	return p == base || strings.HasPrefix(p, base+string(filepath.Separator))
}
func overlaps(a, b string) bool { return within(a, b) || within(b, a) }
func systemPath(p string) bool {
	for _, base := range []string{"/etc", "/nix", "/proc", "/sys", "/dev"} {
		if within(base, p) {
			return true
		}
	}
	return p == "/"
}
func (s *Server) openAllowedRoots() error {
	secret := ""
	if s.config.PasswordFile != "" {
		var err error
		secret, err = filepath.EvalSymlinks(s.config.PasswordFile)
		if err != nil {
			return err
		}
	}
	for _, boundary := range s.config.AllowedRoots {
		if !filepath.IsAbs(boundary.Path) || filepath.Clean(boundary.Path) != boundary.Path {
			return fmt.Errorf("allowed root must be a normalized absolute path")
		}
		real, err := filepath.EvalSymlinks(boundary.Path)
		if err != nil {
			return err
		}
		real = filepath.Clean(real)
		if systemPath(real) {
			return fmt.Errorf("system path cannot be shared: %s", real)
		}
		if overlaps(real, s.config.StateDir) {
			return fmt.Errorf("stateDir must be outside shares and allowed roots")
		}
		if real != boundary.Path {
			return fmt.Errorf("allowed root must not traverse symlinks")
		}
		if secret != "" && within(real, secret) {
			return fmt.Errorf("passwordFile must be outside allowed roots")
		}
		for _, previous := range s.allowedRoots {
			if overlaps(real, previous.Path) {
				return fmt.Errorf("allowed roots must not overlap")
			}
		}
		boundary.Path = real
		boundary.root, err = os.OpenRoot(real)
		if err != nil {
			return err
		}
		s.allowedRoots = append(s.allowedRoots, boundary)
	}
	return nil
}
func (s *Server) openShare(sh Share) (Share, error) {
	sh.root = nil
	sh.err = ""
	if !validName(sh.ID) || strings.TrimSpace(sh.Name) == "" || len(sh.Name) > 255 || !filepath.IsAbs(sh.Path) {
		return sh, fail(400, "共享名称或路径无效")
	}
	sh.Path = filepath.Clean(sh.Path)
	for _, boundary := range s.allowedRoots {
		if !within(boundary.Path, sh.Path) {
			continue
		}
		if boundary.ReadOnly && !sh.ReadOnly {
			return sh, fail(403, "部署范围只读，不能创建可写共享")
		}
		relative, err := filepath.Rel(boundary.Path, sh.Path)
		if err != nil {
			return sh, err
		}
		if relative != "." {
			if _, err = safePath(filepath.ToSlash(relative)); err != nil {
				return sh, err
			}
		}
		if err = noLinks(boundary.root, filepath.ToSlash(relative)); err != nil {
			return sh, err
		}
		sh.root, err = boundary.root.OpenRoot(relative)
		if err != nil {
			return sh, err
		}
		return sh, nil
	}
	return sh, fail(403, "目录不在部署允许范围内")
}
func (s *Server) loadShares() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS shared_settings(id TEXT PRIMARY KEY,name TEXT NOT NULL,path TEXT NOT NULL,read_only INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS settings_meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);`)
	if err != nil {
		return err
	}
	var imported int
	if err = s.db.QueryRow("SELECT count(*) FROM settings_meta WHERE key='shares_imported'").Scan(&imported); err != nil {
		return err
	}
	if imported == 0 {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		initial := []Share{}
		defer func() {
			for _, sh := range initial {
				if sh.root != nil {
					sh.root.Close()
				}
			}
		}()
		ids := map[string]bool{}
		for _, sh := range s.config.Shares {
			if sh.ID == "" {
				h := sha256.Sum256([]byte(filepath.Clean(sh.Path)))
				sh.ID = hex.EncodeToString(h[:16])
			}
			opened, err := s.openShare(sh)
			if err != nil {
				return err
			}
			initial = append(initial, opened)
			if ids[opened.ID] {
				return fmt.Errorf("duplicate share ID")
			}
			ids[opened.ID] = true
			for _, previous := range initial[:len(initial)-1] {
				if overlaps(opened.Path, previous.Path) {
					return fmt.Errorf("overlapping shares are not supported")
				}
			}
			if _, err = tx.Exec("INSERT INTO shared_settings(id,name,path,read_only) VALUES(?,?,?,?)", opened.ID, opened.Name, opened.Path, opened.ReadOnly); err != nil {
				return err
			}
		}
		if _, err = tx.Exec("INSERT INTO settings_meta(key,value) VALUES('shares_imported','1')"); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	rows, err := s.db.Query("SELECT id,name,path,read_only FROM shared_settings ORDER BY rowid")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sh Share
		if err = rows.Scan(&sh.ID, &sh.Name, &sh.Path, &sh.ReadOnly); err != nil {
			return err
		}
		opened, openErr := s.openShare(sh)
		if openErr != nil {
			opened.err = openErr.Error()
		}
		s.shares = append(s.shares, opened)
	}
	return rows.Err()
}

// Hold the root handles for each request, including streaming downloads/uploads.
// Settings uses TryLock so active requests return a conflict rather than waiting.
func (s *Server) shareAPI(pattern string, handler func(http.ResponseWriter, *http.Request) error) {
	s.api(pattern, func(w http.ResponseWriter, r *http.Request) error {
		s.sharesMu.RLock()
		defer s.sharesMu.RUnlock()
		return handler(w, r)
	})
}
func (s *Server) sharedSettings(w http.ResponseWriter, r *http.Request) error {
	s.sharesMu.RLock()
	defer s.sharesMu.RUnlock()
	result := []map[string]any{}
	for _, sh := range s.shares {
		result = append(result, map[string]any{"id": sh.ID, "name": sh.Name, "path": sh.Path, "readOnly": sh.ReadOnly, "available": sh.root != nil, "error": sh.err})
	}
	jsonResponse(w, map[string]any{"shares": result, "allowedRoots": s.allowedRoots})
	return nil
}
func (s *Server) settingsDirectories(w http.ResponseWriter, r *http.Request) error {
	for _, boundary := range s.allowedRoots {
		if boundary.Path != r.URL.Query().Get("root") {
			continue
		}
		p, err := safePath(r.URL.Query().Get("path"))
		if err != nil {
			return err
		}
		if err = noLinks(boundary.root, p); err != nil {
			return err
		}
		dir, err := boundary.root.Open(p)
		if err != nil {
			return err
		}
		defer dir.Close()
		entries, err := dir.ReadDir(-1)
		if err != nil {
			return err
		}
		result := []map[string]string{}
		for _, entry := range entries {
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && !strings.HasPrefix(entry.Name(), ".webui-") {
				result = append(result, map[string]string{"name": entry.Name(), "path": filepath.Join(boundary.Path, p, entry.Name())})
			}
		}
		jsonResponse(w, map[string]any{"path": filepath.Join(boundary.Path, p), "entries": result})
		return nil
	}
	return fail(403, "不允许浏览此目录范围")
}
func (s *Server) shareBusy(id string) bool {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	for _, t := range s.tasks {
		if t.sourceShare == id || t.destinationShare == id {
			return true
		}
	}
	return false
}
func (s *Server) hasTrash(id string) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT count(*) FROM trash WHERE share=?", id).Scan(&count)
	return count > 0, err
}
func (s *Server) saveShare(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		ReadOnly bool   `json:"readOnly"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	if !s.sharesMu.TryLock() {
		return fail(409, "文件请求或上传正在进行，请稍后重试")
	}
	defer s.sharesMu.Unlock()
	index := -1
	if b.ID != "" {
		for i, sh := range s.shares {
			if sh.ID == b.ID {
				index = i
				break
			}
		}
		if index < 0 {
			return fail(404, "共享目录不存在")
		}
	} else {
		b.ID = token()
	}
	if s.shareBusy(b.ID) {
		return fail(409, "共享目录有进行中的任务，请完成或取消后重试")
	}
	sh, err := s.openShare(Share{ID: b.ID, Name: b.Name, Path: b.Path, ReadOnly: b.ReadOnly})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			sh.root.Close()
		}
	}()
	for i, other := range s.shares {
		if i != index && overlaps(other.Path, sh.Path) {
			return fail(409, "共享目录不能重叠")
		}
	}
	if index >= 0 && s.shares[index].Path != sh.Path {
		has, err := s.hasTrash(b.ID)
		if err != nil {
			return err
		}
		if has {
			return fail(409, "回收站非空，请先恢复或永久删除后更改路径")
		}
	}
	_, err = s.db.Exec("INSERT INTO shared_settings(id,name,path,read_only) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,path=excluded.path,read_only=excluded.read_only", sh.ID, sh.Name, sh.Path, sh.ReadOnly)
	if err != nil {
		return err
	}
	if index < 0 {
		s.shares = append(s.shares, sh)
	} else {
		old := s.shares[index]
		s.shares[index] = sh
		if old.root != nil {
			old.root.Close()
		}
	}
	committed = true
	jsonResponse(w, map[string]bool{"ok": true})
	return nil
}
func (s *Server) removeShare(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		ID string `json:"id"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	if !s.sharesMu.TryLock() {
		return fail(409, "文件请求或上传正在进行，请稍后重试")
	}
	defer s.sharesMu.Unlock()
	for i, sh := range s.shares {
		if sh.ID != b.ID {
			continue
		}
		if s.shareBusy(b.ID) {
			return fail(409, "共享目录有进行中的任务，请完成或取消后重试")
		}
		has, err := s.hasTrash(b.ID)
		if err != nil {
			return err
		}
		if has {
			return fail(409, "回收站非空，请先恢复或永久删除后移除共享")
		}
		if _, err = s.db.Exec("DELETE FROM shared_settings WHERE id=?", b.ID); err != nil {
			return err
		}
		if sh.root != nil {
			sh.root.Close()
		}
		s.shares = append(s.shares[:i], s.shares[i+1:]...)
		jsonResponse(w, map[string]bool{"ok": true})
		return nil
	}
	return fail(404, "共享目录不存在")
}
