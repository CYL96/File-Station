package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func within(base, p string) bool {
	return p == base || strings.HasPrefix(p, strings.TrimRight(base, string(filepath.Separator))+string(filepath.Separator))
}
func overlaps(a, b string) bool { return within(a, b) || within(b, a) }

// Host paths may resolve symlinks; paths inside an opened share still use noLinks.
func accessibleDirectory(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fail(400, "目录路径必须是绝对路径")
	}
	p = filepath.Clean(p)
	for _, component := range strings.Split(p, string(filepath.Separator)) {
		if strings.HasPrefix(component, ".webui-") {
			return "", fail(403, "路径不允许访问")
		}
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	for _, component := range strings.Split(real, string(filepath.Separator)) {
		if strings.HasPrefix(component, ".webui-") {
			return "", fail(403, "路径不允许访问")
		}
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fail(400, "路径不是目录")
	}
	// AT_EACCESS asks the kernel to check effective IDs, supplementary groups and
	// ACLs. Opening an os.Root alone does not require read or search permission.
	if err = unix.Faccessat(unix.AT_FDCWD, real, unix.R_OK|unix.X_OK, unix.AT_EACCESS); err != nil {
		return "", &os.PathError{Op: "access", Path: real, Err: err}
	}
	return real, nil
}

// Resolve every accessible prefix without requiring access to the original secret.
// LoadCredential may read a source beneath root-only directories before startup.
func resolvedPrivatePath(p string) string {
	for range 40 {
		for prefix := p; ; prefix = filepath.Dir(prefix) {
			if real, err := filepath.EvalSymlinks(prefix); err == nil {
				suffix, _ := filepath.Rel(prefix, p)
				return filepath.Join(real, suffix)
			}
			if target, err := os.Readlink(prefix); err == nil {
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(prefix), target)
				}
				suffix, _ := filepath.Rel(prefix, p)
				p = filepath.Join(target, suffix)
				break
			}
			if prefix == filepath.Dir(prefix) {
				return p
			}
		}
	}
	return p
}

func (s *Server) sharePrivacy(p string) error {
	if overlaps(p, s.config.StateDir) {
		return fail(403, "stateDir must be outside shares")
	}
	if s.config.PasswordFile != "" && within(p, s.config.PasswordFile) {
		return fail(403, "passwordFile must be outside shares")
	}
	if s.config.PasswordSourceFile != "" && (within(p, s.config.PasswordSourceFile) || within(p, s.passwordSourceReal)) {
		return fail(403, "passwordSourceFile must be outside shares")
	}
	return nil
}

func (s *Server) openShare(sh Share) (Share, error) {
	sh.root = nil
	sh.err = ""
	if !validName(sh.ID) || strings.TrimSpace(sh.Name) == "" || len(sh.Name) > 255 {
		return sh, fail(400, "共享名称或路径无效")
	}
	real, err := accessibleDirectory(sh.Path)
	if err != nil {
		return sh, err
	}
	sh.Path = real
	if err = s.sharePrivacy(real); err != nil {
		return sh, err
	}
	sh.root, err = os.OpenRoot(real)
	return sh, err
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
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	var document string
	err = s.db.QueryRow("SELECT value FROM settings_meta WHERE key='shares_order'").Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var ids []string
	if err = json.Unmarshal([]byte(document), &ids); err != nil {
		return fmt.Errorf("invalid saved share order: %w", err)
	}
	byID := make(map[string]int, len(s.shares))
	for i, sh := range s.shares {
		byID[sh.ID] = i
	}
	ordered := make([]Share, 0, len(s.shares))
	for _, id := range ids {
		if index, ok := byID[id]; ok {
			ordered = append(ordered, s.shares[index])
			delete(byID, id)
		}
	}
	// Ignore stale IDs and append shares absent from older order documents.
	for _, sh := range s.shares {
		if _, ok := byID[sh.ID]; ok {
			ordered = append(ordered, sh)
		}
	}
	s.shares = ordered
	return nil
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
	jsonResponse(w, map[string]any{"shares": result})
	return nil
}

// Caller must hold sharesMu while reading the shared list.
func (s *Server) shareList() []map[string]any {
	result := make([]map[string]any, 0, len(s.shares))
	for _, sh := range s.shares {
		result = append(result, map[string]any{"id": sh.ID, "name": sh.Name, "readOnly": sh.ReadOnly})
	}
	return result
}

func saveShareOrder(tx *sql.Tx, ids []string) error {
	document, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO settings_meta(key,value) VALUES('shares_order',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(document))
	return err
}

func (s *Server) reorderShares(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		IDs []string `json:"ids"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	if !s.sharesMu.TryLock() {
		return fail(409, "文件请求或上传正在进行，请稍后重试")
	}
	defer s.sharesMu.Unlock()
	if b.IDs == nil || len(b.IDs) != len(s.shares) {
		return fail(400, "共享顺序必须包含全部共享且不能重复")
	}
	byID := make(map[string]int, len(s.shares))
	for i, sh := range s.shares {
		byID[sh.ID] = i
	}
	ordered := make([]Share, 0, len(s.shares))
	for _, id := range b.IDs {
		index, ok := byID[id]
		if !ok {
			return fail(400, "共享顺序必须包含全部共享且不能重复")
		}
		ordered = append(ordered, s.shares[index])
		delete(byID, id)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = saveShareOrder(tx, b.IDs); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.shares = ordered
	jsonResponse(w, s.shareList())
	return nil
}

func (s *Server) settingsDirectories(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = string(filepath.Separator)
	}
	real, err := accessibleDirectory(p)
	if err != nil {
		return err
	}
	if within(s.config.StateDir, real) {
		return fail(403, "不允许浏览应用私有目录")
	}
	dir, err := os.Open(real)
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
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".webui-") {
			continue
		}
		child := filepath.Join(real, entry.Name())
		if within(s.config.StateDir, child) {
			continue
		}
		canonical, err := accessibleDirectory(child)
		if err != nil || within(s.config.StateDir, canonical) {
			continue
		}
		result = append(result, map[string]string{"name": entry.Name(), "path": canonical})
	}
	jsonResponse(w, map[string]any{"path": real, "entries": result})
	return nil
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO shared_settings(id,name,path,read_only) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,path=excluded.path,read_only=excluded.read_only", sh.ID, sh.Name, sh.Path, sh.ReadOnly)
	if err != nil {
		return err
	}
	if index < 0 {
		ids := make([]string, len(s.shares)+1)
		for i, current := range s.shares {
			ids[i] = current.ID
		}
		ids[len(s.shares)] = sh.ID
		if err = saveShareOrder(tx, ids); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
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
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.Exec("DELETE FROM shared_settings WHERE id=?", b.ID); err != nil {
			return err
		}
		ids := make([]string, 0, len(s.shares)-1)
		for _, current := range s.shares {
			if current.ID != b.ID {
				ids = append(ids, current.ID)
			}
		}
		if err = saveShareOrder(tx, ids); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
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
