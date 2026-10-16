package server

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type Entry struct {
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	IsDir       bool       `json:"isDir"`
	Size        int64      `json:"size"`
	Modified    time.Time  `json:"modified"`
	IsSymlink   bool       `json:"isSymlink"`
	Extension   string     `json:"extension"`
	Permissions string     `json:"permissions"`
	Owner       string     `json:"owner"`
	Group       string     `json:"group"`
	Created     *time.Time `json:"created"`
}

func (s *Server) query(r *http.Request, write bool) (*Share, string, error) {
	sh, err := s.share(r.URL.Query().Get("share"), write)
	if err != nil {
		return nil, "", err
	}
	p, err := safePath(r.URL.Query().Get("path"))
	return sh, p, err
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) error {
	sh, p, err := s.query(r, false)
	if err != nil {
		return err
	}
	if err = noLinks(sh.root, p); err != nil {
		return err
	}
	dir, err := sh.root.OpenFile(p, os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	result := []Entry{}
	accounts := newListingAccounts()
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".webui-") {
			continue
		}
		entry, err := readEntryMetadata(dir, e.Name(), e.Name(), accounts)
		if err != nil {
			continue
		}
		entry.Path = path.Join(p, e.Name())
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return result[i].Name < result[j].Name
	})
	jsonResponse(w, map[string]any{"path": p, "entries": result})
	return nil
}
func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Share string `json:"share"`
		Path  string `json:"path"`
		Name  string `json:"name"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	sh, err := s.share(b.Share, true)
	if err != nil {
		return err
	}
	p, err := safePath(b.Path)
	if err != nil {
		return err
	}
	if !validName(b.Name) {
		return fail(400, "名称无效")
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	if err = noLinks(sh.root, p); err != nil {
		return err
	}
	name := path.Join(p, b.Name)
	if err = sh.root.Mkdir(name, 0700); err != nil {
		return err
	}
	if err = sh.root.Chmod(name, s.dirMode()); err != nil {
		_ = sh.root.Remove(name)
		return err
	}
	jsonResponse(w, map[string]bool{"ok": true})
	return nil
}

// mkdirParents leaves new restore ancestors private and traversable until the
// caller has restored the inode, then finalizes them in reverse order.
func (s *Server) mkdirParents(root *os.Root, name string) ([]string, error) {
	current := "."
	var created []string
	for _, component := range strings.Split(name, "/") {
		current = path.Join(current, component)
		if err := noLinks(root, current); err != nil && !os.IsNotExist(err) {
			return created, err
		}
		if err := root.Mkdir(current, 0700); err != nil {
			if !os.IsExist(err) {
				return created, err
			}
			info, err := root.Lstat(current)
			if err != nil {
				return created, err
			}
			if !info.IsDir() {
				return created, fail(409, "恢复路径的父级不是目录")
			}
			continue
		}
		created = append(created, current)
	}
	return created, nil
}

func conflictPolicy(p string) error {
	switch p {
	case "", "error", "skip", "replace", "keep":
		return nil
	}
	return fail(400, "冲突策略无效")
}

// Use directory descriptors and RENAME_NOREPLACE: existence checks alone race
// with writers outside this service. Both descriptors are opened under os.Root.
func renameBetween(src *os.Root, from string, dst *os.Root, to string, replace bool) error {
	a, err := src.OpenFile(path.Dir(from), os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer a.Close()
	b, err := dst.OpenFile(path.Dir(to), os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer b.Close()
	flags := uint(unix.RENAME_NOREPLACE)
	if replace {
		flags = 0
	}
	return unix.Renameat2(int(a.Fd()), path.Base(from), int(b.Fd()), path.Base(to), flags)
}
func available(root *os.Root, target, policy string) (string, bool, error) {
	if err := conflictPolicy(policy); err != nil {
		return "", false, err
	}
	_, err := root.Lstat(target)
	if os.IsNotExist(err) {
		return target, false, nil
	}
	if err != nil {
		return "", false, err
	}
	switch policy {
	case "skip":
		return target, true, nil
	case "replace":
		info, err := root.Lstat(target)
		if err != nil {
			return "", false, err
		}
		if info.IsDir() {
			return "", false, fail(409, "不能替换已有目录，请选择保留两份")
		}
		return target, false, nil
	case "keep":
		ext := path.Ext(target)
		base := strings.TrimSuffix(path.Base(target), ext)
		for n := 1; n < 10000; n++ {
			candidate := path.Join(path.Dir(target), fmt.Sprintf("%s (%d)%s", base, n, ext))
			if _, err := root.Lstat(candidate); os.IsNotExist(err) {
				return candidate, false, nil
			} else if err != nil {
				return "", false, err
			}
		}
		return "", false, fail(409, "无法生成新名称")
	default:
		return "", false, fail(409, "目标已存在："+path.Base(target))
	}
}
func (s *Server) rename(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Share    string `json:"share"`
		Path     string `json:"path"`
		Name     string `json:"name"`
		Conflict string `json:"conflict"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	sh, err := s.share(b.Share, true)
	if err != nil {
		return err
	}
	p, err := safePath(b.Path)
	if err != nil {
		return err
	}
	if p == "." || !validName(b.Name) {
		return fail(400, "不能重命名共享根目录或使用无效名称")
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	if err = noLinks(sh.root, path.Dir(p)); err != nil {
		return err
	}
	target := path.Join(path.Dir(p), b.Name)
	if target == p {
		jsonResponse(w, map[string]bool{"ok": true})
		return nil
	}
	target, skip, err := available(sh.root, target, b.Conflict)
	if err != nil {
		return err
	}
	if !skip {
		err = renameBetween(sh.root, p, sh.root, target, b.Conflict == "replace")
	}
	if err != nil {
		return err
	}
	jsonResponse(w, map[string]bool{"ok": true})
	return nil
}
func openRegular(root *os.Root, p string) (*os.File, error) {
	if err := noLinks(root, p); err != nil {
		return nil, err
	}
	f, err := root.OpenFile(p, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fail(403, "只能读取普通文件")
	}
	return f, nil
}
func (s *Server) download(w http.ResponseWriter, r *http.Request) error {
	sh, p, err := s.query(r, false)
	if err != nil {
		return err
	}
	f, err := openRegular(sh.root, p)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", escapeFilename(info.Name())))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	return nil
}
func escapeFilename(name string) string {
	var b strings.Builder
	for _, c := range []byte(name) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
func (s *Server) preview(w http.ResponseWriter, r *http.Request) error {
	sh, p, err := s.query(r, false)
	if err != nil {
		return err
	}
	f, err := openRegular(sh.root, p)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	head := make([]byte, 512)
	n, err := f.Read(head)
	if err != nil && err != io.EOF {
		return err
	}
	kind := http.DetectContentType(head[:n])
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "video/mp4", "video/webm":
		w.Header().Set("Content-Type", kind)
		if _, err = f.Seek(0, 0); err != nil {
			return err
		}
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
		return nil
	}
	if info.Size() > 1<<20 {
		return fail(415, "文本预览限制为 1 MiB")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	text, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(text) > 1<<20 || !utf8.Valid(text) || strings.ContainsRune(string(text), '\x00') {
		return fail(415, "不支持预览此文件")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = w.Write(text)
	return err
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) error {
	sh, p, err := s.query(r, true)
	if err != nil {
		return err
	}
	name := r.URL.Query().Get("name")
	policy := r.URL.Query().Get("conflict")
	if !validName(name) {
		return fail(400, "文件名无效")
	}
	if err = conflictPolicy(policy); err != nil {
		return err
	}
	if err = noLinks(sh.root, p); err != nil {
		return err
	}
	temp := path.Join(p, ".webui-upload-"+token())
	f, err := sh.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer sh.root.Remove(temp)
	defer f.Close()
	_, err = io.Copy(f, r.Body)
	if err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = r.Context().Err(); err != nil {
		return err
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	target, skip, err := available(sh.root, path.Join(p, name), policy)
	if err != nil {
		return err
	}
	if !skip {
		if err = sh.root.Chmod(temp, s.fileMode()); err != nil {
			return err
		}
		err = renameBetween(sh.root, temp, sh.root, target, policy == "replace")
	}
	if err != nil {
		return err
	}
	jsonResponse(w, map[string]bool{"ok": true, "skipped": skip})
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
func (s *Server) copyTree(ctx context.Context, src *os.Root, from string, dst *os.Root, to string, directoryMode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := src.Lstat(from)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fail(403, "不能复制符号链接")
	}
	if info.IsDir() {
		if err = dst.Mkdir(to, 0700); err != nil {
			return err
		}
		dir, err := src.OpenFile(from, os.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		defer dir.Close()
		for {
			entries, e := dir.ReadDir(128)
			for _, child := range entries {
				if strings.HasPrefix(child.Name(), ".webui-") {
					continue
				}
				if err = s.copyTree(ctx, src, path.Join(from, child.Name()), dst, path.Join(to, child.Name()), s.dirMode()); err != nil {
					return err
				}
			}
			if e == io.EOF {
				return dst.Chmod(to, directoryMode)
			}
			if e != nil {
				return e
			}
		}
	}
	if !info.Mode().IsRegular() {
		return fail(403, "不能复制特殊文件")
	}
	in, err := openRegular(src, from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := dst.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, contextReader{ctx, in})
	if err == nil {
		err = out.Sync()
	}
	if err == nil {
		err = out.Chmod(s.fileMode())
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (s *Server) publishCopy(ctx context.Context, src *os.Root, from string, dst *os.Root, to, policy string) error {
	// Keep completed children behind a private directory even after they
	// receive their final, potentially public permissions.
	stage := path.Join(path.Dir(to), ".webui-upload-"+token())
	if err := dst.Mkdir(stage, 0700); err != nil {
		return err
	}
	defer func() {
		// A valid mask may remove owner access. Restore traversal only inside
		// our unpublished stage so cancellation can always remove its subtree.
		_ = fs.WalkDir(dst.FS(), stage, func(p string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return dst.Chmod(p, 0700)
			}
			return nil
		})
		_ = dst.RemoveAll(stage)
	}()
	payload := path.Join(stage, "payload")
	if err := s.copyTree(ctx, src, from, dst, payload, 0700); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := dst.Lstat(payload)
	if err != nil {
		return err
	}
	var directory *os.File
	if info.IsDir() {
		// Moving a directory between parents requires write access to that
		// directory on Linux. Keep its private mode through rename, then use
		// the held descriptor to finalize even a mask that removes all access.
		directory, err = dst.OpenFile(payload, os.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		defer directory.Close()
	}
	if err = renameBetween(dst, payload, dst, to, policy == "replace"); err != nil {
		return err
	}
	if directory != nil {
		return directory.Chmod(s.dirMode())
	}
	return nil
}
