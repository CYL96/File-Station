package server

import (
	"fmt"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Account resolution is scoped to one response, including unsuccessful lookups.
// Host account changes are therefore reflected in subsequent listings.
type listingAccounts struct {
	users  map[uint32]string
	groups map[uint32]string
}

func newListingAccounts() *listingAccounts {
	return &listingAccounts{users: make(map[uint32]string), groups: make(map[uint32]string)}
}

func (a *listingAccounts) owner(id uint32) string {
	if name, ok := a.users[id]; ok {
		return name
	}
	name := strconv.FormatUint(uint64(id), 10)
	if account, err := user.LookupId(name); err == nil && account.Username != "" {
		name = account.Username
	}
	a.users[id] = name
	return name
}

func (a *listingAccounts) group(id uint32) string {
	if name, ok := a.groups[id]; ok {
		return name
	}
	name := strconv.FormatUint(uint64(id), 10)
	if account, err := user.LookupGroupId(name); err == nil && account.Name != "" {
		name = account.Name
	}
	a.groups[id] = name
	return name
}

func fileExtension(name string, isDir bool) string {
	if isDir {
		return ""
	}
	name = path.Base(name)
	// A leading dot alone denotes a dotfile, not a suffix. Ignore empty
	// trailing suffixes so report.TXT. still has the last nonempty suffix TXT.
	name = strings.TrimRight(name, ".")
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		return name[dot+1:]
	}
	return ""
}

// readEntryMetadata only accepts a single child name of an already-open,
// root-constrained directory. Neither syscall follows that child's symlink or
// opens its contents, including for links that point outside the share.
func readEntryMetadata(dir *os.File, name, displayName string, accounts *listingAccounts) (Entry, error) {
	var stat unix.Statx_t
	const basic = unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_UID | unix.STATX_GID | unix.STATX_SIZE | unix.STATX_MTIME
	err := unix.Statx(int(dir.Fd()), name, unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT, unix.STATX_BASIC_STATS|unix.STATX_BTIME, &stat)
	if err == nil && stat.Mask&basic == basic {
		return statxEntry(displayName, &stat, accounts), nil
	}

	// fstatat is descriptor-relative lstat for kernels, policies or filesystems
	// without statx. Never fabricate a creation time from ctime or mtime.
	var legacy unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), name, &legacy, unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT); err != nil {
		return Entry{}, err
	}
	return metadataEntry(displayName, uint16(legacy.Mode), legacy.Size, time.Unix(legacy.Mtim.Sec, legacy.Mtim.Nsec), legacy.Uid, legacy.Gid, nil, accounts), nil
}

func statxEntry(name string, stat *unix.Statx_t, accounts *listingAccounts) Entry {
	var created *time.Time
	if stat.Mask&unix.STATX_BTIME != 0 {
		birth := time.Unix(stat.Btime.Sec, int64(stat.Btime.Nsec)).UTC()
		created = &birth
	}
	return metadataEntry(name, stat.Mode, int64(stat.Size), time.Unix(stat.Mtime.Sec, int64(stat.Mtime.Nsec)), stat.Uid, stat.Gid, created, accounts)
}

func metadataEntry(name string, mode uint16, size int64, modified time.Time, uid, gid uint32, created *time.Time, accounts *listingAccounts) Entry {
	isDir := mode&unix.S_IFMT == unix.S_IFDIR
	return Entry{
		Name: name, IsDir: isDir, Size: size, Modified: modified,
		IsSymlink: mode&unix.S_IFMT == unix.S_IFLNK,
		Extension: fileExtension(name, isDir), Permissions: fmt.Sprintf("%04o", mode&07777),
		Owner: accounts.owner(uid), Group: accounts.group(gid), Created: created,
	}
}
