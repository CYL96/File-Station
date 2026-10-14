package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const textFileLimit = 1 << 20

// JSON can encode each raw byte as six bytes (\u00xx). Allow that expansion
// plus bounded room for paths and the version, then enforce the raw text limit.
const textJSONLimit = 6*textFileLimit + 64*1024

type textSnapshot struct {
	content []byte
	stat    unix.Stat_t
	version string
}

func textPathError(err error, saving bool) error {
	if errors.Is(err, unix.EISDIR) {
		return fail(403, "只能编辑普通文件")
	}
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EXDEV) {
		return fail(403, "不允许跟随符号链接")
	}
	if saving && (errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR)) {
		return fail(409, "文件已删除或替换，请重新打开")
	}
	return err
}

// Pin the directory using the share's root descriptor. Unlike an lstat followed
// by an ordinary open, openat2 rejects symlinks even during concurrent renames.
func textParent(sh *Share, p string, saving bool) (*os.File, error) {
	root, err := sh.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	fd, err := unix.Openat2(int(root.Fd()), path.Dir(p), &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return nil, textPathError(err, saving)
	}
	return os.NewFile(uintptr(fd), path.Dir(p)), nil
}

func textOpen(parent *os.File, name string, saving bool) (*os.File, error) {
	flags := unix.O_RDONLY
	if saving {
		// Opening the existing inode for writing enforces host permissions even
		// though the eventual publication is a directory-level atomic rename.
		flags = unix.O_RDWR
	}
	fd, err := unix.Openat2(int(parent.Fd()), name, &unix.OpenHow{
		Flags:   uint64(flags | unix.O_NONBLOCK | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return nil, textPathError(err, saving)
	}
	f := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		f.Close()
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		f.Close()
		return nil, fail(403, "只能编辑普通文件")
	}
	if saving && stat.Nlink != 1 {
		f.Close()
		return nil, fail(403, "不能原子编辑具有多个硬链接的文件")
	}
	return f, nil
}

func textFingerprint(stat unix.Stat_t) string {
	return fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d", stat.Dev, stat.Ino,
		stat.Size, stat.Mode, stat.Uid, stat.Gid, stat.Nlink,
		stat.Mtim.Sec, stat.Mtim.Nsec, stat.Ctim.Sec, stat.Ctim.Nsec)
}

func textVersion(content []byte, stat unix.Stat_t) string {
	hash := sha256.New()
	_, _ = hash.Write(content)
	_, _ = io.WriteString(hash, "\x00"+textFingerprint(stat))
	return hex.EncodeToString(hash.Sum(nil))
}

func validText(content []byte) bool {
	return len(content) <= textFileLimit && utf8.Valid(content) && !bytes.ContainsRune(content, '\x00')
}

func readTextSnapshot(ctx context.Context, f *os.File) (textSnapshot, error) {
	var result textSnapshot
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := unix.Fstat(int(f.Fd()), &result.stat); err != nil {
		return result, err
	}
	if result.stat.Size > textFileLimit {
		return result, fail(415, "文本编辑限制为 1 MiB")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	content, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, r: f}, textFileLimit+1))
	if err != nil {
		return result, err
	}
	var after unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &after); err != nil {
		return result, err
	}
	if textFingerprint(result.stat) != textFingerprint(after) {
		return result, fail(409, "文件已修改，请重新打开")
	}
	if !validText(content) {
		return result, fail(415, "只支持不含 NUL 的 UTF-8 文本，最大 1 MiB")
	}
	result.content = content
	result.version = textVersion(content, result.stat)
	return result, nil
}

func (s *Server) fileText(w http.ResponseWriter, r *http.Request) error {
	sh, p, err := s.query(r, false)
	if err != nil {
		return err
	}
	parent, err := textParent(sh, p, false)
	if err != nil {
		return err
	}
	defer parent.Close()
	f, err := textOpen(parent, path.Base(p), false)
	if err != nil {
		return err
	}
	defer f.Close()
	snapshot, err := readTextSnapshot(r.Context(), f)
	if err != nil {
		return err
	}
	if err = r.Context().Err(); err != nil {
		return err
	}
	jsonResponse(w, map[string]string{"content": string(snapshot.content), "version": snapshot.version})
	return nil
}

type textSaveRequest struct {
	Share   string  `json:"share"`
	Path    string  `json:"path"`
	Content *string `json:"content"`
	Version string  `json:"version"`
}

func decodeTextSave(w http.ResponseWriter, r *http.Request) (textSaveRequest, error) {
	var body textSaveRequest
	r.Body = http.MaxBytesReader(w, r.Body, textJSONLimit)
	raw, err := io.ReadAll(contextReader{ctx: r.Context(), r: r.Body})
	if err != nil {
		return body, err
	}
	// encoding/json replaces invalid UTF-8 rather than reporting it. Reject it
	// before decoding so an invalid upload cannot silently change its contents.
	if !utf8.Valid(raw) {
		return body, fail(415, "只支持 UTF-8 文本")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&body); err != nil {
		return body, fail(400, "请求格式错误")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return body, fail(400, "请求包含多余数据")
	}
	if body.Content == nil || body.Version == "" {
		return body, fail(400, "需要文本内容及文件版本")
	}
	if !validText([]byte(*body.Content)) {
		return body, fail(415, "只支持不含 NUL 的 UTF-8 文本，最大 1 MiB")
	}
	return body, nil
}

func textAttributes(fd int) (map[string][]byte, error) {
	n, err := unix.Flistxattr(fd, nil)
	if err != nil {
		return nil, err
	}
	names := make([]byte, n)
	if n > 0 {
		n, err = unix.Flistxattr(fd, names)
		if err != nil {
			return nil, err
		}
	}
	attrs := make(map[string][]byte)
	for _, name := range strings.Split(string(names[:n]), "\x00") {
		if name == "" {
			continue
		}
		size, err := unix.Fgetxattr(fd, name, nil)
		if err != nil {
			return nil, err
		}
		value := make([]byte, size)
		size, err = unix.Fgetxattr(fd, name, value)
		if err != nil {
			return nil, err
		}
		attrs[name] = value[:size]
	}
	return attrs, nil
}

func preserveTextMetadata(source, target *os.File, stat unix.Stat_t) error {
	attrs, err := textAttributes(int(source.Fd()))
	if err != nil {
		return fail(403, "无法读取文件扩展属性，未保存："+err.Error())
	}
	fd := int(target.Fd())
	var targetStat unix.Stat_t
	if err = unix.Fstat(fd, &targetStat); err != nil {
		return err
	}
	if targetStat.Uid != stat.Uid || targetStat.Gid != stat.Gid {
		if err = unix.Fchown(fd, int(stat.Uid), int(stat.Gid)); err != nil {
			return fail(403, "无法保留文件所有者，未保存："+err.Error())
		}
	}
	if err = unix.Fchmod(fd, stat.Mode&07777); err != nil {
		return err
	}
	inherited, err := textAttributes(fd)
	if err != nil {
		return fail(403, "无法保留文件扩展属性，未保存："+err.Error())
	}
	for name := range inherited {
		if _, ok := attrs[name]; !ok {
			if err = unix.Fremovexattr(fd, name); err != nil {
				return fail(403, "无法移除继承的扩展属性，未保存："+err.Error())
			}
		}
	}
	for name, value := range attrs {
		if bytes.Equal(inherited[name], value) {
			if _, exists := inherited[name]; exists {
				continue
			}
		}
		if err = unix.Fsetxattr(fd, name, value, 0); err != nil {
			return fail(403, "无法保留文件扩展属性，未保存："+err.Error())
		}
	}
	// ACL updates can affect mode bits; privileged mode bits may also be
	// stripped by the kernel. Refuse publication if preservation was incomplete.
	if err = unix.Fstat(fd, &targetStat); err != nil {
		return err
	}
	if targetStat.Mode&07777 != stat.Mode&07777 || targetStat.Uid != stat.Uid || targetStat.Gid != stat.Gid {
		return fail(403, "无法完整保留文件权限，未保存")
	}
	actual, err := textAttributes(fd)
	if err != nil || len(actual) != len(attrs) {
		return fail(403, "无法完整保留文件扩展属性，未保存")
	}
	for name, value := range attrs {
		got, exists := actual[name]
		if !exists || !bytes.Equal(got, value) {
			return fail(403, "无法完整保留文件扩展属性，未保存")
		}
	}
	return nil
}

func checkTextTarget(ctx context.Context, sh *Share, p string, parent *os.File, version string) error {
	currentParent, err := textParent(sh, p, true)
	if err != nil {
		return err
	}
	defer currentParent.Close()
	var original, current unix.Stat_t
	if err = unix.Fstat(int(parent.Fd()), &original); err != nil {
		return err
	}
	if err = unix.Fstat(int(currentParent.Fd()), &current); err != nil {
		return err
	}
	if original.Dev != current.Dev || original.Ino != current.Ino {
		return fail(409, "文件目录已替换，请重新打开")
	}
	f, err := textOpen(parent, path.Base(p), true)
	if err != nil {
		return err
	}
	defer f.Close()
	snapshot, err := readTextSnapshot(ctx, f)
	if err != nil {
		var api *apiError
		if errors.As(err, &api) && api.status == 415 {
			return fail(409, "文件已修改，请重新打开")
		}
		return err
	}
	if snapshot.version != version {
		return fail(409, "文件已修改，请重新打开")
	}
	return nil
}

// Wait for internal mutations without trapping a canceled HTTP request behind
// a long-running file operation.
func (s *Server) lockTextMutation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.mutation.TryLock() {
		return nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if s.mutation.TryLock() {
				return nil
			}
		}
	}
}

func (s *Server) saveFileText(w http.ResponseWriter, r *http.Request) error {
	body, err := decodeTextSave(w, r)
	if err != nil {
		return err
	}
	sh, err := s.share(body.Share, true)
	if err != nil {
		return err
	}
	p, err := safePath(body.Path)
	if err != nil {
		return err
	}
	if err = r.Context().Err(); err != nil {
		return err
	}
	if err = s.lockTextMutation(r.Context()); err != nil {
		return err
	}
	defer s.mutation.Unlock()
	if err = r.Context().Err(); err != nil {
		return err
	}
	parent, err := textParent(sh, p, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	source, err := textOpen(parent, path.Base(p), true)
	if err != nil {
		return err
	}
	defer source.Close()
	snapshot, err := readTextSnapshot(r.Context(), source)
	if err != nil {
		var api *apiError
		if errors.As(err, &api) && api.status == 415 {
			return fail(409, "文件已修改，请重新打开")
		}
		return err
	}
	if snapshot.version != body.Version {
		return fail(409, "文件已修改，请重新打开")
	}
	tempName := ".webui-text-" + token()
	fd, err := unix.Openat(int(parent.Fd()), tempName, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(int(parent.Fd()), tempName, 0)
	temp := os.NewFile(uintptr(fd), tempName)
	defer temp.Close()
	if _, err = io.Copy(temp, contextReader{ctx: r.Context(), r: strings.NewReader(*body.Content)}); err != nil {
		return err
	}
	if err = preserveTextMetadata(source, temp, snapshot.stat); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	// Other processes do not participate in mutation. Recheck both the path
	// identity and contents immediately before rename. This narrows but cannot
	// eliminate the external check-to-rename race: rename is not filesystem CAS.
	if err = checkTextTarget(r.Context(), sh, p, parent, body.Version); err != nil {
		return err
	}
	if err = r.Context().Err(); err != nil {
		return err
	}
	if err = unix.Renameat(int(parent.Fd()), tempName, int(parent.Fd()), path.Base(p)); err != nil {
		return textPathError(err, true)
	}
	var published unix.Stat_t
	if err = unix.Fstat(fd, &published); err != nil {
		return err
	}
	jsonResponse(w, map[string]string{"version": textVersion([]byte(*body.Content), published)})
	return nil
}
