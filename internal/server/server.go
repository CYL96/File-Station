package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Share struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly"`
	root     *os.Root
	err      string
}
type Config struct {
	IP                 string  `json:"ip"`
	Port               int     `json:"port"`
	StateDir           string  `json:"stateDir"`
	Umask              string  `json:"umask"`
	Password           string  `json:"password,omitempty"`
	PasswordFile       string  `json:"passwordFile,omitempty"`
	PasswordSourceFile string  `json:"passwordSourceFile,omitempty"`
	SecureCookies      bool    `json:"secureCookies"`
	Shares             []Share `json:"shares"`
}

func (c Config) Address() string {
	return net.JoinHostPort(c.IP, strconv.Itoa(c.Port))
}

type Server struct {
	config             Config
	umask              os.FileMode
	passwordSourceReal string
	db                 *sql.DB
	password           [32]byte
	mux                *http.ServeMux
	mutation           sync.Mutex
	sharesMu           sync.RWMutex
	shares             []Share
	tasksMu            sync.Mutex
	tasks              map[string]*Task
	workers            sync.WaitGroup
	loginMu            sync.Mutex
	failures           int
	blockedUntil       time.Time
}
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string           { return e.message }
func fail(status int, message string) error { return &apiError{status, message} }
func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func jsonResponse(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func errorResponse(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	var e *apiError
	if errors.As(err, &e) {
		code = e.status
	} else if errors.Is(err, fs.ErrPermission) {
		code = 403
	} else if errors.Is(err, fs.ErrNotExist) {
		code = 404
	} else if errors.Is(err, fs.ErrExist) {
		code = 409
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fail(400, "请求格式错误")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return fail(400, "请求包含多余数据")
	}
	return nil
}

func New(c Config, static string) (*Server, error) {
	if c.Umask == "" {
		c.Umask = "0022"
	}
	if len(c.Umask) != 3 && len(c.Umask) != 4 {
		return nil, fmt.Errorf("umask must contain 3 or 4 octal digits between 0000 and 0777")
	}
	for _, digit := range c.Umask {
		if digit < '0' || digit > '7' {
			return nil, fmt.Errorf("umask must contain 3 or 4 octal digits between 0000 and 0777")
		}
	}
	mask, err := strconv.ParseUint(c.Umask, 8, 12)
	if err != nil || mask > 0777 {
		return nil, fmt.Errorf("umask must contain 3 or 4 octal digits between 0000 and 0777")
	}
	if net.ParseIP(c.IP) == nil || c.Port < 1 || c.Port > 65535 {
		return nil, fmt.Errorf("ip must be an IPv4 or IPv6 address and port must be between 1 and 65535")
	}
	if !filepath.IsAbs(c.StateDir) {
		return nil, fmt.Errorf("absolute stateDir required")
	}
	if (c.Password == "") == (c.PasswordFile == "") {
		return nil, fmt.Errorf("configure exactly one of password or passwordFile")
	}
	secret := []byte(c.Password)
	if c.PasswordFile != "" {
		secret, err = os.ReadFile(c.PasswordFile)
		if err != nil {
			return nil, err
		}
		c.PasswordFile, err = filepath.Abs(c.PasswordFile)
		if err != nil {
			return nil, err
		}
		c.PasswordFile, err = filepath.EvalSymlinks(c.PasswordFile)
		if err != nil {
			return nil, err
		}
		secret = []byte(strings.TrimRight(string(secret), "\r\n"))
	}
	if c.PasswordSourceFile != "" {
		c.PasswordSourceFile, err = filepath.Abs(c.PasswordSourceFile)
		if err != nil {
			return nil, err
		}
	}
	if len(secret) < 6 {
		return nil, fmt.Errorf("password must contain at least 6 bytes")
	}
	if err = os.MkdirAll(c.StateDir, 0700); err != nil {
		return nil, err
	}
	c.StateDir, err = filepath.EvalSymlinks(c.StateDir)
	if err != nil {
		return nil, err
	}
	c.Password = ""
	s := &Server{config: c, umask: os.FileMode(mask), password: sha256.Sum256(secret), tasks: map[string]*Task{}, mux: http.NewServeMux()}
	if c.PasswordSourceFile != "" {
		s.passwordSourceReal = resolvedPrivatePath(c.PasswordSourceFile)
	}
	// Check the initial import before creating a database beneath an invalid share.
	// A saved database remains authoritative over later bootstrap configuration.
	if _, statErr := os.Stat(filepath.Join(c.StateDir, "state.sqlite")); errors.Is(statErr, fs.ErrNotExist) {
		for _, sh := range c.Shares {
			real, openErr := accessibleDirectory(sh.Path)
			if openErr != nil {
				return nil, openErr
			}
			if openErr = s.sharePrivacy(real); openErr != nil {
				return nil, openErr
			}
		}
	}
	if err = os.Chmod(c.StateDir, 0700); err != nil {
		s.Close()
		return nil, err
	}
	s.db, err = sql.Open("sqlite", filepath.Join(c.StateDir, "state.sqlite"))
	if err != nil {
		s.Close()
		return nil, err
	}
	s.db.SetMaxOpenConns(1)
	_, err = s.db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY,csrf TEXT NOT NULL,expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS trash(id TEXT PRIMARY KEY,share TEXT NOT NULL,path TEXT NOT NULL,deleted_at TEXT NOT NULL,size INTEGER NOT NULL,is_dir INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY,kind TEXT NOT NULL,status TEXT NOT NULL,completed INTEGER NOT NULL,total INTEGER NOT NULL,error TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS task_operations(id TEXT PRIMARY KEY,body TEXT NOT NULL);
 UPDATE tasks SET status='error',error='服务重启，任务已中断；请检查源与目标后重试' WHERE status='running';
 DELETE FROM sessions WHERE expires < unixepoch();`)
	if err != nil {
		s.Close()
		return nil, err
	}
	if err = os.Chmod(filepath.Join(c.StateDir, "state.sqlite"), 0600); err != nil {
		s.Close()
		return nil, err
	}
	if err = s.loadShares(); err != nil {
		s.Close()
		return nil, err
	}
	s.routes(static)
	return s, nil
}
func (s *Server) fileMode() os.FileMode { return 0666 &^ s.umask }
func (s *Server) dirMode() os.FileMode  { return 0777 &^ s.umask }

func (s *Server) Close() {
	s.CancelTasks()
	s.workers.Wait()
	for _, sh := range s.shares {
		if sh.root != nil {
			_ = sh.root.Close()
		}
	}
	if s.db != nil {
		_ = s.db.Close()
	}
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob:; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'")
	s.mux.ServeHTTP(w, r)
}
func (s *Server) originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
}
func (s *Server) session(r *http.Request) (string, error) {
	cookie, err := r.Cookie("file_station_session")
	if err != nil {
		return "", fail(401, "请先登录")
	}
	var csrf string
	err = s.db.QueryRow("SELECT csrf FROM sessions WHERE token=? AND expires>?", cookie.Value, time.Now().Unix()).Scan(&csrf)
	if err != nil {
		return "", fail(401, "会话已过期，请重新登录")
	}
	return csrf, nil
}
func (s *Server) api(pattern string, handler func(http.ResponseWriter, *http.Request) error) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		csrf, err := s.session(r)
		if err == nil && r.Method != "GET" {
			if !s.originOK(r) || subtle.ConstantTimeCompare([]byte(csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
				err = fail(403, "请求校验失败")
			}
		}
		if err == nil {
			err = handler(w, r)
		}
		if err != nil {
			errorResponse(w, err)
		}
	})
}
func (s *Server) routes(static string) {
	s.mux.HandleFunc("POST /api/login", s.login)
	s.api("GET /api/session", func(w http.ResponseWriter, r *http.Request) error {
		csrf, err := s.session(r)
		if err != nil {
			return err
		}
		jsonResponse(w, map[string]string{"username": "admin", "csrf": csrf})
		return nil
	})
	s.api("POST /api/logout", func(w http.ResponseWriter, r *http.Request) error {
		cookie, _ := r.Cookie("file_station_session")
		_, err := s.db.Exec("DELETE FROM sessions WHERE token=?", cookie.Value)
		http.SetCookie(w, &http.Cookie{Name: "file_station_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.config.SecureCookies})
		jsonResponse(w, map[string]bool{"ok": true})
		return err
	})
	s.api("GET /api/shares", func(w http.ResponseWriter, r *http.Request) error {
		result := []map[string]any{}
		s.sharesMu.RLock()
		defer s.sharesMu.RUnlock()
		for _, sh := range s.shares {
			result = append(result, map[string]any{"id": sh.ID, "name": sh.Name, "readOnly": sh.ReadOnly})
		}
		jsonResponse(w, result)
		return nil
	})
	s.shareAPI("GET /api/files", s.list)
	s.shareAPI("POST /api/files/size", s.calculateSize)
	s.shareAPI("GET /api/files/text", s.fileText)
	s.shareAPI("POST /api/files/text/save", s.saveFileText)
	s.shareAPI("POST /api/mkdir", s.mkdir)
	s.shareAPI("POST /api/rename", s.rename)
	s.shareAPI("POST /api/upload", s.upload)
	s.shareAPI("GET /api/download", s.download)
	s.shareAPI("GET /api/preview", s.preview)
	s.shareAPI("POST /api/operation", s.operation)
	s.api("GET /api/tasks", s.getTasks)
	s.api("POST /api/tasks/clear", s.clearTasks)
	s.api("POST /api/tasks/cancel", s.cancelTask)
	s.shareAPI("POST /api/tasks/retry", s.retryTask)
	s.api("GET /api/events", s.events)
	s.shareAPI("GET /api/trash", s.listTrash)
	s.shareAPI("POST /api/trash/restore", s.restoreTrash)
	s.shareAPI("POST /api/trash/delete", s.deleteTrash)
	s.api("GET /api/settings/files", s.fileSettings)
	s.api("POST /api/settings/files/save", s.saveFileSettings)
	s.api("GET /api/settings/shares", s.sharedSettings)
	s.api("GET /api/settings/directories", s.settingsDirectories)
	s.api("POST /api/settings/shares/save", s.saveShare)
	s.api("POST /api/settings/shares/remove", s.removeShare)
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { errorResponse(w, fail(404, "接口不存在")) })
	files := http.FileServer(http.Dir(static))
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-store")
		}
		files.ServeHTTP(w, r)
	})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.originOK(r) {
		errorResponse(w, fail(403, "不允许跨站登录"))
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		errorResponse(w, err)
		return
	}
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if time.Now().Before(s.blockedUntil) {
		errorResponse(w, fail(429, "登录失败过多，请一分钟后重试"))
		return
	}
	hash := sha256.Sum256([]byte(body.Password))
	if subtle.ConstantTimeCompare(hash[:], s.password[:]) != 1 {
		s.failures++
		if s.failures >= 10 {
			s.blockedUntil = time.Now().Add(time.Minute)
			s.failures = 0
		}
		errorResponse(w, fail(401, "密码错误"))
		return
	}
	s.failures = 0
	t, csrf := token(), token()
	_, err := s.db.Exec("INSERT INTO sessions VALUES(?,?,?)", t, csrf, time.Now().Add(12*time.Hour).Unix())
	if err != nil {
		errorResponse(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "file_station_session", Value: t, Path: "/", MaxAge: 43200, HttpOnly: true, Secure: s.config.SecureCookies, SameSite: http.SameSiteStrictMode})
	jsonResponse(w, map[string]string{"username": "admin", "csrf": csrf})
}
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00") && !strings.HasPrefix(name, ".webui-")
}
func safePath(value string) (string, error) {
	if value == "" || value == "." {
		return ".", nil
	}
	if strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00") {
		return "", fail(400, "路径无效")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || strings.HasPrefix(part, ".webui-") {
			return "", fail(403, "路径不允许访问")
		}
	}
	return path.Clean(value), nil
}
func (s *Server) share(id string, write bool) (*Share, error) {
	for i := range s.shares {
		sh := &s.shares[i]
		if sh.ID == id {
			if sh.root == nil {
				return nil, fail(403, "共享目录不可用："+sh.err)
			}
			if write && sh.ReadOnly {
				return nil, fail(403, "共享目录只读")
			}
			return sh, nil
		}
	}
	return nil, fail(404, "共享目录不存在")
}
func noLinks(root *os.Root, p string) error {
	prefix := ""
	for _, part := range strings.Split(p, "/") {
		prefix = path.Join(prefix, part)
		info, err := root.Lstat(prefix)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fail(403, "不允许跟随符号链接")
		}
	}
	return nil
}
