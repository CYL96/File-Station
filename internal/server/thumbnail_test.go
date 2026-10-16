package server

import (
	"bytes"
	"container/list"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newThumbnailFixture(t *testing.T) (*fileSettingsFixture, string, string) {
	t.Helper()
	f := newFileSettingsFixture(t)
	root := filepath.Join(filepath.Dir(f.config.StateDir), "pictures")
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(map[string]any{"name": "Images", "path": root, "readOnly": true})
	f.request("POST", "/api/settings/shares/save", string(document), http.StatusOK)
	var shares []Share
	if err := json.Unmarshal(f.request("GET", "/api/shares", "", http.StatusOK), &shares); err != nil || len(shares) != 1 {
		t.Fatalf("shares: %v, %v", shares, err)
	}
	return f, root, shares[0].ID
}

func thumbnailRequest(t *testing.T, f *fileSettingsFixture, client *http.Client, method, endpoint, share, name, extra string) (*http.Response, []byte) {
	t.Helper()
	address := f.srv.URL + "/api/" + endpoint + "?share=" + url.QueryEscape(share) + "&path=" + url.QueryEscape(name) + extra
	req, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("missing no-store: %v", response.Header)
	}
	return response, body
}

func thumbnailWrite(t *testing.T, root, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), data, 0640); err != nil {
		t.Fatal(err)
	}
}

func thumbnailEncode(t *testing.T, kind string, source image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	var err error
	switch kind {
	case "jpeg":
		err = jpeg.Encode(&out, source, &jpeg.Options{Quality: 95})
	case "png":
		err = png.Encode(&out, source)
	case "gif":
		err = gif.Encode(&out, source, nil)
	default:
		t.Fatalf("unknown fixture format %s", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func thumbnailSolid(width, height int, value color.NRGBA) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			out.SetNRGBA(x, y, value)
		}
	}
	return out
}

func thumbnailDecode(t *testing.T, response *http.Response, data []byte, width, height int, kind string) image.Image {
	t.Helper()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/"+kind {
		t.Fatalf("thumbnail status/headers: %d %v: %s", response.StatusCode, response.Header, data)
	}
	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || format != kind {
		t.Fatalf("decode thumbnail: format=%s error=%v", format, err)
	}
	if decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height {
		t.Fatalf("thumbnail bounds=%v, want %dx%d", decoded.Bounds(), width, height)
	}
	return decoded
}

func TestThumbnailFormatsHTTP(t *testing.T) {
	f, root, share := newThumbnailFixture(t)
	for _, test := range []struct {
		name, input, output                  string
		width, height, wantWidth, wantHeight int
		alpha                                uint8
	}{
		{"landscape", "jpeg", "jpeg", 800, 400, 320, 160, 255},
		{"portrait", "png", "jpeg", 400, 800, 160, 320, 255},
		{"small", "png", "jpeg", 7, 3, 7, 3, 255},
		{"transparent", "png", "png", 800, 400, 320, 160, 128},
		{"gif", "gif", "jpeg", 640, 320, 320, 160, 255},
		{"one-pixel-edge", "png", "jpeg", 1, 1000, 1, 320, 255},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := thumbnailEncode(t, test.input, thumbnailSolid(test.width, test.height, color.NRGBA{R: 220, G: 30, B: 10, A: test.alpha}))
			name := test.name + ".data"
			thumbnailWrite(t, root, name, original)
			response, raw := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, name, "&modified=untrusted&size=1")
			decoded := thumbnailDecode(t, response, raw, test.wantWidth, test.wantHeight, test.output)
			_, _, _, alpha := decoded.At(0, 0).RGBA()
			if test.alpha == 128 && (alpha < 0x7f00 || alpha > 0x8200) {
				t.Fatalf("transparency lost: %x", alpha)
			}
			response, head := thumbnailRequest(t, f, f.client, "HEAD", "thumbnail", share, name, "")
			if response.StatusCode != http.StatusOK || len(head) != 0 || response.ContentLength != int64(len(raw)) {
				t.Fatalf("HEAD=%d length=%d body=%d", response.StatusCode, response.ContentLength, len(head))
			}
			_, cached := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, name, "&modified=different&size=2")
			if !bytes.Equal(raw, cached) {
				t.Fatal("client metadata changed server cache output")
			}
			for _, endpoint := range []string{"preview", "download"} {
				response, full := thumbnailRequest(t, f, f.client, "GET", endpoint, share, name, "")
				if response.StatusCode != http.StatusOK || !bytes.Equal(full, original) {
					t.Fatalf("%s changed original image", endpoint)
				}
			}
			stored, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || !bytes.Equal(stored, original) {
				t.Fatalf("original modified: %v", err)
			}
		})
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 6 {
		t.Fatalf("thumbnail created files in read-only share: %v, %v", entries, err)
	}
	f.app.thumbnails.mu.Lock()
	defer f.app.thumbnails.mu.Unlock()
	if len(f.app.thumbnails.entries) != 6 {
		t.Fatalf("unexpected cache entries: %d", len(f.app.thumbnails.entries))
	}
}

func TestThumbnailGIFFirstFrameHTTP(t *testing.T) {
	f, root, share := newThumbnailFixture(t)
	palette := color.Palette{color.NRGBA{}, color.NRGBA{R: 255, A: 255}, color.NRGBA{B: 255, A: 255}}
	first := image.NewPaletted(image.Rect(3, 2, 9, 6), palette)
	for i := range first.Pix {
		first.Pix[i] = 1
	}
	first.SetColorIndex(3, 2, 0)
	second := image.NewPaletted(image.Rect(0, 0, 12, 8), palette)
	for i := range second.Pix {
		second.Pix[i] = 2
	}
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{1, 1}, Config: image.Config{ColorModel: palette, Width: 12, Height: 8}}); err != nil {
		t.Fatal(err)
	}
	thumbnailWrite(t, root, "animation.gif", encoded.Bytes())
	response, raw := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, "animation.gif", "")
	decoded := thumbnailDecode(t, response, raw, 6, 4, "png")
	if _, _, _, alpha := decoded.At(0, 0).RGBA(); alpha != 0 {
		t.Fatal("offset GIF first-frame transparency lost")
	}
	red, _, blue, _ := decoded.At(5, 3).RGBA()
	if red < 60000 || blue > 1000 {
		t.Fatal("GIF thumbnail is not first frame")
	}
}

func thumbnailEXIF(orientation int, order binary.ByteOrder) []byte {
	data := make([]byte, 26)
	copy(data, "II")
	if order == binary.BigEndian {
		copy(data, "MM")
	}
	order.PutUint16(data[2:4], 42)
	order.PutUint32(data[4:8], 8)
	order.PutUint16(data[8:10], 1)
	order.PutUint16(data[10:12], 0x0112)
	order.PutUint16(data[12:14], 3)
	order.PutUint32(data[14:18], 1)
	order.PutUint16(data[18:20], uint16(orientation))
	return data
}

func TestThumbnailEXIFOrientationHTTP(t *testing.T) {
	f, root, share := newThumbnailFixture(t)
	colors := []color.NRGBA{{R: 240, A: 255}, {G: 240, A: 255}, {B: 240, A: 255}, {R: 240, G: 240, A: 255}}
	source := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	for y := range 40 {
		for x := range 80 {
			source.SetNRGBA(x, y, colors[(y/20)*2+x/40])
		}
	}
	jpegData := thumbnailEncode(t, "jpeg", source)
	// Corner labels are independent expected EXIF transforms, not calls to the
	// production orientation helper: TL, TR, BL, BR.
	corners := [8][4]int{{0, 1, 2, 3}, {1, 0, 3, 2}, {3, 2, 1, 0}, {2, 3, 0, 1}, {0, 2, 1, 3}, {2, 0, 3, 1}, {3, 1, 2, 0}, {1, 3, 0, 2}}
	for index, labels := range corners {
		orientation := index + 1
		t.Run(fmt.Sprint(orientation), func(t *testing.T) {
			order := binary.ByteOrder(binary.LittleEndian)
			if orientation%2 == 0 {
				order = binary.BigEndian
			}
			metadata := append([]byte("Exif\x00\x00"), thumbnailEXIF(orientation, order)...)
			segment := []byte{0xff, 0xe1, 0, 0}
			binary.BigEndian.PutUint16(segment[2:4], uint16(len(metadata)+2))
			original := append(append(append([]byte{}, jpegData[:2]...), segment...), metadata...)
			original = append(original, jpegData[2:]...)
			name := fmt.Sprintf("orientation-%d.jpg", orientation)
			thumbnailWrite(t, root, name, original)
			width, height := 80, 40
			if orientation >= 5 {
				width, height = height, width
			}
			response, raw := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, name, "")
			decoded := thumbnailDecode(t, response, raw, width, height, "jpeg")
			points := []image.Point{{5, 5}, {width - 6, 5}, {5, height - 6}, {width - 6, height - 6}}
			for i, point := range points {
				got := color.NRGBAModel.Convert(decoded.At(point.X, point.Y)).(color.NRGBA)
				want := colors[labels[i]]
				if abs(int(got.R)-int(want.R)) > 25 || abs(int(got.G)-int(want.G)) > 25 || abs(int(got.B)-int(want.B)) > 25 {
					t.Fatalf("corner%d=%v want %v", i, got, want)
				}
			}
		})
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func TestOrientThumbnailNonzeroBounds(t *testing.T) {
	source := image.NewNRGBA(image.Rect(7, 11, 10, 13))
	for y := range 2 {
		for x := range 3 {
			source.SetNRGBA(7+x, 11+y, color.NRGBA{R: uint8(y*3 + x + 1), A: uint8(30 + x + y)})
		}
	}
	want := [][]uint8{{1, 2, 3, 4, 5, 6}, {3, 2, 1, 6, 5, 4}, {6, 5, 4, 3, 2, 1}, {4, 5, 6, 1, 2, 3}, {1, 4, 2, 5, 3, 6}, {4, 1, 5, 2, 6, 3}, {6, 3, 5, 2, 4, 1}, {3, 6, 2, 5, 1, 4}}
	for orientation, expected := range want {
		out := orientThumbnail(source, orientation+1)
		var actual []uint8
		for y := out.Bounds().Min.Y; y < out.Bounds().Max.Y; y++ {
			for x := out.Bounds().Min.X; x < out.Bounds().Max.X; x++ {
				actual = append(actual, out.NRGBAAt(x, y).R)
			}
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("orientation%d=%v want %v", orientation+1, actual, expected)
		}
	}
}

func TestThumbnailCachedSecurityAndInvalidationHTTP(t *testing.T) {
	f, root, share := newThumbnailFixture(t)
	red := thumbnailEncode(t, "png", thumbnailSolid(30, 20, color.NRGBA{R: 255, A: 255}))
	blue := thumbnailEncode(t, "png", thumbnailSolid(30, 20, color.NRGBA{B: 255, A: 255}))
	thumbnailWrite(t, root, "photo.png", red)
	_, initial := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, "photo.png", "")
	response, _ := thumbnailRequest(t, f, f.srv.Client(), "GET", "thumbnail", share, "photo.png", "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous cached thumbnail=%d", response.StatusCode)
	}
	before, err := os.Stat(filepath.Join(root, "photo.png"))
	if err != nil {
		t.Fatal(err)
	}
	thumbnailWrite(t, root, "photo.png", blue)
	if err := os.Chtimes(filepath.Join(root, "photo.png"), before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	response, changed := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, "photo.png", "")
	thumbnailDecode(t, response, changed, 30, 20, "jpeg")
	if bytes.Equal(initial, changed) {
		t.Fatal("mtime-preserving file rewrite returned stale thumbnail")
	}
	if err := os.Rename(filepath.Join(root, "photo.png"), filepath.Join(root, "target.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.png", filepath.Join(root, "photo.png")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		status int
	}{
		{"photo.png", 403}, {"../target.png", 403}, {".webui-private.png", 403}, {"missing.png", 404}, {".", 403},
	} {
		response, _ := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, test.name, "")
		if response.StatusCode != test.status {
			t.Fatalf("%s status=%d want%d", test.name, response.StatusCode, test.status)
		}
	}
	other := filepath.Join(filepath.Dir(root), "other-pictures")
	if err := os.Mkdir(other, 0750); err != nil {
		t.Fatal(err)
	}
	thumbnailWrite(t, other, "photo.png", red)
	document, _ := json.Marshal(map[string]any{"id": share, "name": "Moved", "path": other, "readOnly": true})
	f.request("POST", "/api/settings/shares/save", string(document), http.StatusOK)
	response, moved := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, "photo.png", "")
	thumbnailDecode(t, response, moved, 30, 20, "jpeg")
	if !bytes.Equal(initial, moved) {
		t.Fatal("share path change did not use new root")
	}
	document, _ = json.Marshal(map[string]string{"id": share})
	f.request("POST", "/api/settings/shares/remove", string(document), http.StatusOK)
	response, _ = thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, "photo.png", "")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("removed cached share=%d", response.StatusCode)
	}
}

func TestThumbnailRejectsDamagedAndOversizeHTTP(t *testing.T) {
	f, root, share := newThumbnailFixture(t)
	pngData := thumbnailEncode(t, "png", thumbnailSolid(1, 1, color.NRGBA{A: 255}))
	oversize := bytes.Clone(pngData)
	binary.BigEndian.PutUint32(oversize[16:20], 200_000)
	binary.BigEndian.PutUint32(oversize[20:24], 200_000)
	binary.BigEndian.PutUint32(oversize[29:33], crc32.ChecksumIEEE(oversize[12:29]))
	gifOversize := append([]byte("GIF89a"), 0xff, 0xff, 0xff, 0xff, 0, 0, 0)
	// A tiny VP8X canvas followed by an oversized actual VP8L frame must be
	// rejected before its decoder attempts an allocation.
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00"), make([]byte, 10)...)
	webp = append(webp, []byte("VP8L\x05\x00\x00\x00\x2f\xff\xff\xff\x0f\x00")...)
	binary.LittleEndian.PutUint32(webp[4:8], uint32(len(webp)-8))
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"oversize.png", oversize}, {"oversize.gif", gifOversize}, {"mismatched.webp", webp},
		{"truncated.png", pngData[:33]}, {"unsupported.jpg", []byte("not an image")},
		{"broken.jpg", []byte{0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff}},
	} {
		t.Run(test.name, func(t *testing.T) {
			thumbnailWrite(t, root, test.name, test.data)
			response, raw := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, test.name, "")
			if response.StatusCode != http.StatusUnsupportedMediaType || response.Header.Get("Content-Type") != "application/json" || bytes.Equal(raw, test.data) {
				t.Fatalf("bad image response=%d %v %s", response.StatusCode, response.Header, raw)
			}
		})
	}
	thumbnailWrite(t, root, "unsupported.jpg", pngData)
	response, raw := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, "unsupported.jpg", "")
	thumbnailDecode(t, response, raw, 1, 1, "jpeg")
}

func TestThumbnailTIFFMalformedOffsets(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		good := thumbnailEXIF(8, order)
		for cut := range len(good) {
			want := 1
			if cut >= 22 {
				want = 8
			}
			if got := thumbnailTIFFOrientation(good[:cut]); got != want {
				t.Fatalf("truncated metadata returned%d want%d", got, want)
			}
		}
		for _, offset := range []uint32{0, 7, 25, 0xffffffff} {
			bad := bytes.Clone(good)
			order.PutUint32(bad[4:8], offset)
			if got := thumbnailTIFFOrientation(bad); got != 1 {
				t.Fatalf("bad offset%d returned%d", offset, got)
			}
		}
		bad := bytes.Clone(good)
		order.PutUint16(bad[8:10], 65535)
		if thumbnailTIFFOrientation(bad) != 1 {
			t.Fatal("oversize IFD accepted")
		}
	}
	for _, data := range [][]byte{{}, {0xff, 0xd8, 0xff, 0xe1, 0, 1}, {0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff}} {
		if thumbnailJPEGOrientation(bytes.NewReader(data)) != 1 {
			t.Fatal("malformed JPEG orientation")
		}
	}
	for _, dimensions := range [][2]int{{0, 1}, {-1, 1}, {32_000_001, 1}, {int(^uint(0) >> 1), int(^uint(0) >> 1)}, {8000, 4001}} {
		if thumbnailDimensions(dimensions[0], dimensions[1]) {
			t.Fatalf("unsafe dimensions accepted%v", dimensions)
		}
	}
	if !thumbnailDimensions(8000, 4000) {
		t.Fatal("exact pixel limit rejected")
	}
}

func TestThumbnailCacheEvictionAccounting(t *testing.T) {
	cache := thumbnailCache{entries: make(map[thumbnailKey]*list.Element)}
	for i := range 3 {
		cache.insert(thumbnailKey{path: fmt.Sprint(i)}, thumbnailImage{data: make([]byte, 1, 8)}, 16)
	}
	if cache.bytes != 16 || len(cache.entries) != 2 || cache.entries[thumbnailKey{path: "0"}] != nil {
		t.Fatalf("FIFO accounting bytes=%d entries=%d", cache.bytes, len(cache.entries))
	}
	cache.insert(thumbnailKey{path: "too-large"}, thumbnailImage{data: make([]byte, 1, 17)}, 16)
	if cache.bytes != 16 || len(cache.entries) != 2 {
		t.Fatal("oversized backing allocation admitted")
	}
	cache = thumbnailCache{entries: make(map[thumbnailKey]*list.Element)}
	for i := range thumbnailCacheEntries + 5 {
		cache.insert(thumbnailKey{path: fmt.Sprint(i)}, thumbnailImage{}, thumbnailCacheBytes)
	}
	if len(cache.entries) != thumbnailCacheEntries || cache.fifo.Len() != thumbnailCacheEntries {
		t.Fatal("cache metadata entry limit not enforced")
	}
}

// Signals when get has reached its duplicate-wait select. It wraps a real
// cancelable context, without sleeping or relying on scheduler timing.
type thumbnailWaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *thumbnailWaitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func thumbnailWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("thumbnail operation deadlocked")
	}
}

func TestThumbnailCacheDedupAndCancellation(t *testing.T) {
	var cache thumbnailCache
	key := thumbnailKey{path: "same"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ownerStarted, ownerDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(ownerDone)
		_, err := cache.get(ctx, key, func() (thumbnailImage, error) {
			close(ownerStarted)
			<-ctx.Done()
			return thumbnailImage{}, ctx.Err()
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("owner error=%v", err)
		}
	}()
	thumbnailWait(t, ownerStarted)
	waiting := &thumbnailWaitingContext{Context: context.Background(), waiting: make(chan struct{})}
	waiterDone := make(chan struct{})
	var generations atomic.Int32
	go func() {
		defer close(waiterDone)
		value, err := cache.get(waiting, key, func() (thumbnailImage, error) {
			generations.Add(1)
			return thumbnailImage{data: []byte("recovered")}, nil
		})
		if err != nil || string(value.data) != "recovered" {
			t.Errorf("waiter value=%s error=%v", value.data, err)
		}
	}()
	thumbnailWait(t, waiting.waiting)
	cancel()
	thumbnailWait(t, ownerDone)
	thumbnailWait(t, waiterDone)
	value, err := cache.get(context.Background(), key, func() (thumbnailImage, error) {
		t.Error("cache hit generated duplicate")
		return thumbnailImage{}, nil
	})
	if err != nil || string(value.data) != "recovered" || generations.Load() != 1 {
		t.Fatal("canceled owner poisoned cache")
	}

	key = thumbnailKey{path: "waiter-cancel"}
	ownerStarted, ownerDone = make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	go func() {
		defer close(ownerDone)
		_, err := cache.get(context.Background(), key, func() (thumbnailImage, error) {
			close(ownerStarted)
			<-release
			return thumbnailImage{data: []byte("ok")}, nil
		})
		if err != nil {
			t.Errorf("owner error=%v", err)
		}
	}()
	thumbnailWait(t, ownerStarted)
	waitContext, waitCancel := context.WithCancel(context.Background())
	waiting = &thumbnailWaitingContext{Context: waitContext, waiting: make(chan struct{})}
	waiterDone = make(chan struct{})
	go func() {
		defer close(waiterDone)
		_, err := cache.get(waiting, key, func() (thumbnailImage, error) {
			t.Error("duplicate waiter generated")
			return thumbnailImage{}, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled waiter error=%v", err)
		}
	}()
	thumbnailWait(t, waiting.waiting)
	waitCancel()
	thumbnailWait(t, waiterDone)
	close(release)
	thumbnailWait(t, ownerDone)
}

func TestThumbnailCacheGenerationAndAdmissionBounds(t *testing.T) {
	var cache thumbnailCache
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var owners sync.WaitGroup
	for i := range 2 {
		owners.Add(1)
		go func() {
			defer owners.Done()
			_, err := cache.get(context.Background(), thumbnailKey{path: fmt.Sprint(i)}, func() (thumbnailImage, error) {
				started <- struct{}{}
				<-release
				return thumbnailImage{data: []byte{1}}, nil
			})
			if err != nil {
				t.Errorf("generator error=%v", err)
			}
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("generators failed to start")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiting := &thumbnailWaitingContext{Context: ctx, waiting: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := cache.get(waiting, thumbnailKey{path: "third"}, func() (thumbnailImage, error) {
			t.Error("more than two active generators")
			return thumbnailImage{}, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("queued cancellation=%v", err)
		}
	}()
	thumbnailWait(t, waiting.waiting)
	cancel()
	thumbnailWait(t, done)
	close(release)
	owners.Wait()
	for range thumbnailRequests {
		cache.requests <- struct{}{}
	}
	_, err := cache.get(context.Background(), thumbnailKey{path: "overloaded"}, func() (thumbnailImage, error) {
		t.Error("overloaded cache generated")
		return thumbnailImage{}, nil
	})
	var apiErr *apiError
	if !errors.As(err, &apiErr) || apiErr.status != 503 {
		t.Fatalf("admission error=%v", err)
	}
	for range thumbnailRequests {
		<-cache.requests
	}
	failure := errors.New("decoder failed")
	key := thumbnailKey{path: "retry"}
	_, err = cache.get(context.Background(), key, func() (thumbnailImage, error) { return thumbnailImage{}, failure })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	value, err := cache.get(context.Background(), key, func() (thumbnailImage, error) { return thumbnailImage{data: []byte("retry works")}, nil })
	if err != nil || string(value.data) != "retry works" {
		t.Fatalf("failed generation cached forever: %v", err)
	}
}

func TestThumbnailWebPHTTP(t *testing.T) {
	f, root, share := newThumbnailFixture(t)
	// Real 4x2 Chromium canvas WebP encodings, embedded to avoid any dependency
	// on a developer module cache, external encoder, or downloaded testdata.
	for _, test := range []struct{ name, encoded, kind string }{
		{"opaque", "UklGRi4AAABXRUJQVlA4WAoAAAAAAAAAAwAAAQAAVlA4TA8AAAAvA0AAAAcQ/Y/+ByKi/wEA", "jpeg"},
		{"alpha", "UklGRi4AAABXRUJQVlA4WAoAAAAQAAAAAwAAAQAAVlA4TA8AAAAvA0AAEAcQ/Y8CBiKi/wEA", "png"},
	} {
		t.Run(test.name, func(t *testing.T) {
			original, err := base64.StdEncoding.DecodeString(test.encoded)
			if err != nil {
				t.Fatal(err)
			}
			name := test.name + ".webp"
			thumbnailWrite(t, root, name, original)
			response, raw := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, name, "")
			decoded := thumbnailDecode(t, response, raw, 4, 2, test.kind)
			got := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA)
			if got.R < 230 || got.G > 20 || got.B > 20 {
				t.Fatalf("WebP color changed: %v", got)
			}
			if test.kind == "png" && (got.A < 126 || got.A > 130) {
				t.Fatalf("WebP alpha lost: %v", got)
			}
			response, full := thumbnailRequest(t, f, f.client, "GET", "preview", share, name, "")
			if response.StatusCode != http.StatusOK || !bytes.Equal(full, original) {
				t.Fatal("WebP preview changed bytes")
			}
		})
	}
}

func TestThumbnailCacheSameKeyDedup(t *testing.T) {
	var cache thumbnailCache
	key := thumbnailKey{path: "deduplicate"}
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := cache.get(context.Background(), key, func() (thumbnailImage, error) {
			close(started)
			<-release
			return thumbnailImage{data: []byte("same-result")}, nil
		})
		if err != nil {
			t.Errorf("owner error=%v", err)
		}
	}()
	thumbnailWait(t, started)
	waiting := &thumbnailWaitingContext{Context: context.Background(), waiting: make(chan struct{})}
	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		value, err := cache.get(waiting, key, func() (thumbnailImage, error) {
			t.Error("same-key request regenerated")
			return thumbnailImage{}, nil
		})
		if err != nil || string(value.data) != "same-result" {
			t.Errorf("waiter data=%s err=%v", value.data, err)
		}
	}()
	thumbnailWait(t, waiting.waiting)
	close(release)
	thumbnailWait(t, done)
	thumbnailWait(t, waiterDone)
}

func TestThumbnailCachePanicReleasesFlightAndGenerator(t *testing.T) {
	var cache thumbnailCache
	key := thumbnailKey{path: "panicking-decoder"}
	started, release, ownerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(ownerDone)
		defer func() {
			if recover() != "decoder panic" {
				t.Error("decoder panic was unexpectedly swallowed")
			}
		}()
		_, _ = cache.get(context.Background(), key, func() (thumbnailImage, error) {
			close(started)
			<-release
			panic("decoder panic")
		})
	}()
	thumbnailWait(t, started)
	waiting := &thumbnailWaitingContext{Context: context.Background(), waiting: make(chan struct{})}
	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		_, err := cache.get(waiting, key, func() (thumbnailImage, error) {
			t.Error("waiting duplicate regenerated")
			return thumbnailImage{}, nil
		})
		var apiErr *apiError
		if !errors.As(err, &apiErr) || apiErr.status != 415 {
			t.Errorf("panic waiter error=%v", err)
		}
	}()
	thumbnailWait(t, waiting.waiting)
	close(release)
	thumbnailWait(t, ownerDone)
	thumbnailWait(t, waiterDone)
	cache.mu.Lock()
	if len(cache.flights) != 0 || len(cache.generators) != 0 || len(cache.requests) != 0 {
		t.Error("panic stranded thumbnail state")
	}
	cache.mu.Unlock()
	value, err := cache.get(context.Background(), key, func() (thumbnailImage, error) { return thumbnailImage{data: []byte("healthy")}, nil })
	if err != nil || string(value.data) != "healthy" {
		t.Fatalf("generation after panic=%s %v", value.data, err)
	}
}

func TestThumbnailWebPAlphaCanvasMismatchHTTP(t *testing.T) {
	f, root, share := newThumbnailFixture(t)
	// Genuine Chromium 4x2 lossy VP8 payload, also checked through the endpoint
	// below. The malformed file keeps it intact but claims a 1x1 alpha canvas.
	original, err := base64.StdEncoding.DecodeString("UklGRk4AAABXRUJQVlA4WAoAAAAAAAAAAwAAAQAAVlA4IDAAAADQAQCdASoEAAIAAkA4JaACdLoB+AADsAD+8JtD/yC0rlvAH/8SF+JC/Ehf+LmAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	malformed := bytes.Clone(original[:30])
	malformed[20] = 0x10
	clear(malformed[24:30])
	malformed = append(malformed, []byte("ALPH\x02\x00\x00\x00\x00\xff")...)
	malformed = append(malformed, original[30:]...)
	binary.LittleEndian.PutUint32(malformed[4:8], uint32(len(malformed)-8))
	thumbnailWrite(t, root, "mismatch.webp", malformed)
	for range 3 {
		response, _ := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, "mismatch.webp", "")
		if response.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("mismatched alpha canvas status=%d", response.StatusCode)
		}
	}
	thumbnailWrite(t, root, "valid.webp", original)
	response, raw := thumbnailRequest(t, f, f.client, "GET", "thumbnail", share, "valid.webp", "")
	thumbnailDecode(t, response, raw, 4, 2, "jpeg")
	f.app.thumbnails.mu.Lock()
	defer f.app.thumbnails.mu.Unlock()
	if len(f.app.thumbnails.flights) != 0 || len(f.app.thumbnails.generators) != 0 {
		t.Fatal("malformed image stranded generation state")
	}
}
