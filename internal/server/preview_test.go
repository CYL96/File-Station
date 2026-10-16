package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestVideoPreviewHTTP(t *testing.T) {
	f := newFileSettingsFixture(t)
	root := filepath.Join(filepath.Dir(f.config.StateDir), "files")
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	shareJSON, err := json.Marshal(map[string]any{"name": "Read only", "path": root, "readOnly": true})
	if err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/settings/shares/save", string(shareJSON), http.StatusOK)
	var shares []Share
	if err := json.Unmarshal(f.request("GET", "/api/shares", "", http.StatusOK), &shares); err != nil {
		t.Fatal(err)
	}
	if len(shares) != 1 || !shares[0].ReadOnly {
		t.Fatalf("expected one read-only share, got %+v", shares)
	}
	request := func(t *testing.T, client *http.Client, method, name, byteRange string) (*http.Response, []byte) {
		t.Helper()
		path := "/api/preview?share=" + url.QueryEscape(shares[0].ID) + "&path=" + url.QueryEscape(name)
		req, err := http.NewRequest(method, f.srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if byteRange != "" {
			req.Header.Set("Range", byteRange)
		}
		// GET and HEAD use only the session cookie, as a native video element does.
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp, body
	}

	// These are sniffable container headers with deterministic payload, not
	// playable video fixtures. The filenames intentionally have no video suffix.
	for _, container := range []struct {
		name, contentType, header string
	}{
		{"mp4", "video/mp4", "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"},
		{"webm", "video/webm", "\x1a\x45\xdf\xa3\x87\x42\x82\x84webm"},
	} {
		t.Run(container.name, func(t *testing.T) {
			payload := []byte("preview-payload\x00\x01\xfe\n")
			content := append([]byte(container.header), bytes.Repeat(payload, (1<<20)/len(payload)+1)...)
			name := "clip-" + container.name + ".data"
			if err := os.WriteFile(filepath.Join(root, name), content, 0640); err != nil {
				t.Fatal(err)
			}
			t.Run("GET", func(t *testing.T) {
				resp, body := request(t, f.client, "GET", name, "")
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("GET status = %d, want 200", resp.StatusCode)
				}
				if resp.Header.Get("Content-Type") != container.contentType || resp.ContentLength != int64(len(content)) {
					t.Fatalf("GET content headers = %v, length = %d", resp.Header, resp.ContentLength)
				}
				if resp.Header.Get("Accept-Ranges") != "bytes" {
					t.Fatalf("GET missing range support: %v", resp.Header)
				}
				if !bytes.Equal(body, content) {
					t.Fatal("GET changed content or lost the sniffed header")
				}
			})
			t.Run("HEAD", func(t *testing.T) {
				resp, body := request(t, f.client, "HEAD", name, "")
				if resp.StatusCode != http.StatusOK || len(body) != 0 {
					t.Fatalf("HEAD status = %d, body length = %d", resp.StatusCode, len(body))
				}
				if resp.Header.Get("Content-Type") != container.contentType || resp.ContentLength != int64(len(content)) {
					t.Fatalf("HEAD content headers = %v, length = %d", resp.Header, resp.ContentLength)
				}
			})
			for _, span := range []struct {
				name, header string
				start, end   int
			}{
				{"header", "bytes=0-31", 0, 31},
				{"sniff-boundary", "bytes=509-521", 509, 521},
				{"suffix", "bytes=-19", len(content) - 19, len(content) - 1},
			} {
				t.Run("range-"+span.name, func(t *testing.T) {
					resp, body := request(t, f.client, "GET", name, span.header)
					wantRange := fmt.Sprintf("bytes %d-%d/%d", span.start, span.end, len(content))
					if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") != wantRange {
						t.Fatalf("Range %s: status = %d, Content-Range = %q, want %q", span.header, resp.StatusCode, resp.Header.Get("Content-Range"), wantRange)
					}
					if resp.ContentLength != int64(span.end-span.start+1) || !bytes.Equal(body, content[span.start:span.end+1]) {
						t.Fatalf("Range %s: length = %d, body = %x", span.header, resp.ContentLength, body)
					}
				})
			}
			t.Run("range-unsatisfiable", func(t *testing.T) {
				resp, _ := request(t, f.client, "GET", name, fmt.Sprintf("bytes=%d-", len(content)))
				wantRange := fmt.Sprintf("bytes */%d", len(content))
				if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable || resp.Header.Get("Content-Range") != wantRange {
					t.Fatalf("unsatisfiable range: status = %d, Content-Range = %q, want %q", resp.StatusCode, resp.Header.Get("Content-Range"), wantRange)
				}
			})
		})
	}

	t.Run("authentication", func(t *testing.T) {
		for _, method := range []string{"GET", "HEAD"} {
			t.Run(method, func(t *testing.T) {
				resp, _ := request(t, f.srv.Client(), method, "clip-mp4.data", "")
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("anonymous %s status = %d, want 401", method, resp.StatusCode)
				}
			})
		}
	})
	t.Run("symlink", func(t *testing.T) {
		if err := os.Symlink(filepath.Join(root, "clip-mp4.data"), filepath.Join(root, "link.mp4")); err != nil {
			t.Fatal(err)
		}
		resp, _ := request(t, f.client, "GET", "link.mp4", "")
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("symlink preview status = %d, want 403", resp.StatusCode)
		}
	})
	for _, text := range []struct {
		name    string
		content []byte
		status  int
	}{
		{"disguised.mp4", []byte("This is text, not an MP4 video.\n"), http.StatusOK},
		{"text-limit.mp4", bytes.Repeat([]byte("t"), 1<<20), http.StatusOK},
		{"text-over-limit.mp4", bytes.Repeat([]byte("t"), (1<<20)+1), http.StatusUnsupportedMediaType},
	} {
		t.Run(text.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(root, text.name), text.content, 0640); err != nil {
				t.Fatal(err)
			}
			resp, body := request(t, f.client, "GET", text.name, "")
			if resp.StatusCode != text.status {
				t.Fatalf("text preview status = %d, want %d", resp.StatusCode, text.status)
			}
			if text.status == http.StatusOK && (resp.Header.Get("Content-Type") != "text/plain; charset=utf-8" || !bytes.Equal(body, text.content)) {
				t.Fatal(".mp4 filename changed text preview type or content")
			}
		})
	}
}
