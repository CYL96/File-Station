package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

type sizeTotals struct {
	Size    int64 `json:"size"`
	Files   int64 `json:"files"`
	Folders int64 `json:"folders"`
	Skipped int64 `json:"skipped"`
}
type sizeItem struct {
	Path string `json:"path"`
	sizeTotals
}

// Descriptor-relative, no-follow traversal pins each entry before inspecting it.
// O_PATH avoids opening FIFOs/devices or requiring read permission on file data.
func measureEntry(ctx context.Context, fd int, totals *sizeTotals) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil {
		return err
	}
	switch info.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		totals.Size += info.Size
		totals.Files++
	case unix.S_IFDIR:
		totals.Folders++
		dirFD, err := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		dir := os.NewFile(uintptr(dirFD), "size-directory")
		defer dir.Close()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			entries, readErr := dir.ReadDir(128)
			if readErr != nil && readErr != io.EOF {
				return readErr
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".webui-") {
					continue
				}
				child, err := unix.Openat(dirFD, entry.Name(), unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
				if err != nil {
					return fmt.Errorf("%s: %w", entry.Name(), err)
				}
				err = measureEntry(ctx, child, totals)
				unix.Close(child)
				if err != nil {
					return fmt.Errorf("%s: %w", entry.Name(), err)
				}
			}
			if readErr == io.EOF {
				break
			}
		}
	default:
		totals.Skipped++
	}
	return nil
}

func (s *Server) calculateSize(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Share string   `json:"share"`
		Paths []string `json:"paths"`
		Trash bool     `json:"trash"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if len(body.Paths) == 0 || len(body.Paths) > 1000 {
		return fail(400, "请选择 1 到 1000 项")
	}
	share, err := s.share(body.Share, false)
	if err != nil {
		return err
	}
	paths := make([]string, len(body.Paths))
	seen := make(map[string]bool, len(paths))
	for i, value := range body.Paths {
		if body.Trash {
			if !validName(value) {
				return fail(400, "回收站条目无效")
			}
			var original string
			if err := s.db.QueryRowContext(r.Context(), "SELECT path FROM trash WHERE id=? AND share=?", value, share.ID).Scan(&original); err != nil {
				return fail(404, "回收站条目不存在")
			}
			paths[i] = path.Join(".webui-trash", value, "data")
		} else {
			paths[i], err = safePath(value)
			if err != nil {
				return err
			}
		}
		if seen[paths[i]] {
			return fail(400, "不能重复选择文件")
		}
		seen[paths[i]] = true
	}
	for _, p := range paths {
		for parent := path.Dir(p); parent != p; parent = path.Dir(parent) {
			if seen[parent] {
				return fail(400, "不能同时选择父目录和子项")
			}
			if parent == "." {
				break
			}
		}
	}
	root, err := share.root.Open(".")
	if err != nil {
		return err
	}
	defer root.Close()
	result := struct {
		Items []sizeItem `json:"items"`
		sizeTotals
	}{Items: make([]sizeItem, 0, len(paths))}
	for i, p := range paths {
		fd, err := unix.Openat2(int(root.Fd()), p, &unix.OpenHow{Flags: unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS})
		if err != nil {
			return fmt.Errorf("%s: %w", body.Paths[i], err)
		}
		item := sizeItem{Path: body.Paths[i]}
		err = measureEntry(r.Context(), fd, &item.sizeTotals)
		unix.Close(fd)
		if err != nil {
			return fmt.Errorf("%s: %w", body.Paths[i], err)
		}
		result.Items = append(result.Items, item)
		result.Size += item.Size
		result.Files += item.Files
		result.Folders += item.Folders
		result.Skipped += item.Skipped
	}
	jsonResponse(w, result)
	return nil
}
