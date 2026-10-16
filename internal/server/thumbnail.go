package server

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"encoding/binary"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	thumbnailEdge         = 320
	thumbnailPixels       = 32_000_000
	thumbnailCacheBytes   = 64 << 20
	thumbnailCacheEntries = 4096
	thumbnailRequests     = 64
	thumbnailGenerators   = 2
)

type thumbnailFingerprint struct {
	size          int64
	modified      time.Time
	device, inode uint64
	changed       syscall.Timespec
}

func thumbnailIdentity(info os.FileInfo) thumbnailFingerprint {
	id := thumbnailFingerprint{size: info.Size(), modified: info.ModTime()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		id.device, id.inode, id.changed = uint64(stat.Dev), stat.Ino, stat.Ctim
	}
	return id
}

type thumbnailKey struct {
	share, rootPath, path string
	// A reopened/replaced root must not reuse entries from its predecessor,
	// including hard-linked files with otherwise identical inode metadata.
	root *os.Root
	file thumbnailFingerprint
}

type thumbnailImage struct {
	data        []byte // Immutable after publication, including to waiting requests.
	contentType string
}

type thumbnailEntry struct {
	key   thumbnailKey
	image thumbnailImage
}

type thumbnailFlight struct {
	done  chan struct{}
	image thumbnailImage
	err   error
}

// Zero-value ready. No workers or cleanup goroutines outlive HTTP requests.
// FIFO eviction accounts for the entire backing allocation, not just its length.
type thumbnailCache struct {
	mu         sync.Mutex
	entries    map[thumbnailKey]*list.Element
	fifo       list.List
	bytes      int
	flights    map[thumbnailKey]*thumbnailFlight
	requests   chan struct{}
	generators chan struct{}
}

func (c *thumbnailCache) insert(key thumbnailKey, value thumbnailImage, limit int) {
	cost := cap(value.data)
	if cost > limit {
		return
	}
	for c.bytes+cost > limit || len(c.entries) >= thumbnailCacheEntries {
		oldest := c.fifo.Front()
		entry := oldest.Value.(thumbnailEntry)
		delete(c.entries, entry.key)
		c.bytes -= cap(entry.image.data)
		c.fifo.Remove(oldest)
	}
	c.entries[key] = c.fifo.PushBack(thumbnailEntry{key: key, image: value})
	c.bytes += cost
}

func (c *thumbnailCache) get(ctx context.Context, key thumbnailKey, generate func() (thumbnailImage, error)) (thumbnailImage, error) {
	c.mu.Lock()
	if c.requests == nil {
		c.requests = make(chan struct{}, thumbnailRequests)
		c.generators = make(chan struct{}, thumbnailGenerators)
		c.entries = make(map[thumbnailKey]*list.Element)
		c.flights = make(map[thumbnailKey]*thumbnailFlight)
	}
	c.mu.Unlock()
	// Admission is nonblocking: decoding, queued owners, and duplicate waiters
	// together have a fixed bound, even when the client keeps sending requests.
	select {
	case c.requests <- struct{}{}:
		defer func() { <-c.requests }()
	default:
		return thumbnailImage{}, fail(503, "缩略图请求过多，请稍后重试")
	}
	for {
		if err := ctx.Err(); err != nil {
			return thumbnailImage{}, err
		}
		c.mu.Lock()
		if entry := c.entries[key]; entry != nil {
			value := entry.Value.(thumbnailEntry).image
			c.mu.Unlock()
			return value, nil
		}
		if flight := c.flights[key]; flight != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return thumbnailImage{}, ctx.Err()
			case <-flight.done:
				// A canceled owner must not poison independent waiting requests.
				if errors.Is(flight.err, context.Canceled) || errors.Is(flight.err, context.DeadlineExceeded) {
					continue
				}
				return flight.image, flight.err
			}
		}
		flight := &thumbnailFlight{done: make(chan struct{})}
		c.flights[key] = flight
		c.mu.Unlock()

		func() {
			// A library panic is still propagated to net/http, but it must not
			// strand duplicate waiters or permanently consume a generator slot.
			flight.err = fail(415, "图片无法解码，文件可能已损坏或不是受支持的图片")
			defer func() {
				c.mu.Lock()
				defer c.mu.Unlock()
				if flight.err == nil {
					c.insert(key, flight.image, thumbnailCacheBytes)
				}
				delete(c.flights, key)
				close(flight.done)
			}()
			select {
			case c.generators <- struct{}{}:
				defer func() { <-c.generators }()
				if err := ctx.Err(); err != nil {
					flight.err = err
				} else {
					flight.image, flight.err = generate()
				}
			case <-ctx.Done():
				flight.err = ctx.Err()
			}
			if err := ctx.Err(); err != nil {
				flight.image, flight.err = thumbnailImage{}, err
			}
		}()
		return flight.image, flight.err
	}
}

func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request) error {
	sh, p, err := s.query(r, false)
	if err != nil {
		return err
	}
	// Never trust a cached entry as authorization: redo confinement, symlink,
	// regular-file, and access checks through the current share on every hit.
	f, err := openRegular(sh.root, p)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	key := thumbnailKey{share: sh.ID, rootPath: sh.Path, path: p, root: sh.root, file: thumbnailIdentity(info)}
	value, err := s.thumbnails.get(r.Context(), key, func() (thumbnailImage, error) {
		result, err := generateThumbnail(r.Context(), f)
		if err != nil {
			return thumbnailImage{}, err
		}
		// A writer may operate outside this process. Do not publish a decode
		// spanning changes under the fingerprint observed before decoding.
		after, err := f.Stat()
		if err != nil {
			return thumbnailImage{}, err
		}
		if thumbnailIdentity(after) != key.file {
			return thumbnailImage{}, fail(409, "图片已更改，请重新加载缩略图")
		}
		return result, nil
	})
	if err != nil {
		return err
	}
	// Also cover changes while this request waited on another generator.
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if thumbnailIdentity(after) != key.file {
		return fail(409, "图片已更改，请重新加载缩略图")
	}
	w.Header().Set("Content-Type", value.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(value.data)))
	if r.Method != http.MethodHead {
		_, err = w.Write(value.data)
	}
	return err
}

type thumbnailReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r thumbnailReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func thumbnailDimensions(width, height int) bool {
	return width > 0 && height > 0 && width <= thumbnailPixels && height <= thumbnailPixels && int64(width)*int64(height) <= thumbnailPixels
}

func generateThumbnail(ctx context.Context, f *os.File) (thumbnailImage, error) {
	invalid := func() (thumbnailImage, error) {
		if err := ctx.Err(); err != nil {
			return thumbnailImage{}, err
		}
		return thumbnailImage{}, fail(415, "图片无法解码，文件可能已损坏或不是受支持的图片")
	}
	reader := thumbnailReader{ctx: ctx, reader: f}
	// Replay the exact validated header bytes into Decode, so an external
	// in-place header rewrite cannot bypass the pixel limit between the calls.
	var prefix bytes.Buffer
	config, format, err := image.DecodeConfig(io.TeeReader(io.LimitReader(reader, 1<<20), &prefix))
	if err != nil {
		return invalid()
	}
	if format != "jpeg" && format != "png" && format != "gif" && format != "webp" {
		return invalid()
	}
	if !thumbnailDimensions(config.Width, config.Height) {
		return thumbnailImage{}, fail(415, "图片过大，无法生成缩略图")
	}
	orientation := 1
	if format == "jpeg" {
		orientation = thumbnailJPEGOrientation(thumbnailReader{ctx: ctx, reader: io.NewSectionReader(f, 0, 1<<20)})
	}
	var input io.Reader = io.MultiReader(bytes.NewReader(prefix.Bytes()), reader)
	if format == "webp" {
		// VP8X canvas dimensions can disagree with the later VP8/VP8L frame.
		// Inspect each actual frame header before handing it to the decoder.
		input = &thumbnailWebPReader{reader: input}
	}
	// image/gif.Decode (registered with image.Decode) reads only the first frame.
	source, _, err := image.Decode(input)
	if err != nil {
		return invalid()
	}
	bounds := source.Bounds()
	if !thumbnailDimensions(bounds.Dx(), bounds.Dy()) {
		return invalid()
	}
	if err := ctx.Err(); err != nil {
		return thumbnailImage{}, err
	}
	width, height := bounds.Dx(), bounds.Dy()
	if width > thumbnailEdge || height > thumbnailEdge {
		if width >= height {
			height = max(1, int(int64(height)*thumbnailEdge/int64(width)))
			width = thumbnailEdge
		} else {
			width = max(1, int(int64(width)*thumbnailEdge/int64(height)))
			height = thumbnailEdge
		}
	}
	// Conversion and orientation allocate only thumbnail-sized buffers, never
	// another full-resolution RGBA image alongside the decoder's allocation.
	small := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(small, small.Bounds(), source, bounds, draw.Src, nil)
	small = orientThumbnail(small, orientation)
	var encoded bytes.Buffer
	contentType := "image/jpeg"
	if small.Opaque() {
		err = jpeg.Encode(&encoded, small, &jpeg.Options{Quality: 75})
	} else {
		contentType = "image/png"
		err = png.Encode(&encoded, small)
	}
	if err != nil {
		return invalid()
	}
	return thumbnailImage{data: encoded.Bytes(), contentType: contentType}, nil
}

// This streaming guard does not decode pixels or retain compressed payloads.
// It caps both canvas and actual frame dimensions before x/image allocates.
type thumbnailWebPReader struct {
	reader                    io.Reader
	header                    [30]byte
	pending                   []byte
	remaining                 int64
	started                   bool
	canvasWidth, canvasHeight int
}

func (r *thumbnailWebPReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.pending) != 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	if r.remaining > 0 {
		n, err := r.reader.Read(p[:min(int64(len(p)), r.remaining)])
		r.remaining -= int64(n)
		return n, err
	}
	length := 12
	if r.started {
		length = 8
	}
	if _, err := io.ReadFull(r.reader, r.header[:length]); err != nil {
		return 0, err
	}
	if !r.started {
		r.started = true
		r.pending = r.header[:length]
		return r.Read(p)
	}
	chunk := string(r.header[:4])
	size := int64(binary.LittleEndian.Uint32(r.header[4:8]))
	probe := int64(0)
	switch chunk {
	case "VP8X", "VP8 ":
		probe = 10
	case "VP8L":
		probe = 5
	}
	if size < probe {
		return 0, fail(415, "图片无法解码，文件可能已损坏或不是受支持的图片")
	}
	if _, err := io.ReadFull(r.reader, r.header[8:8+probe]); err != nil {
		return 0, err
	}
	width, height := 1, 1
	switch chunk {
	case "VP8X":
		width = 1 + int(r.header[12]) + int(r.header[13])<<8 + int(r.header[14])<<16
		height = 1 + int(r.header[15]) + int(r.header[16])<<8 + int(r.header[17])<<16
		r.canvasWidth, r.canvasHeight = width, height
	case "VP8 ":
		width = int(binary.LittleEndian.Uint16(r.header[14:16]) & 0x3fff)
		height = int(binary.LittleEndian.Uint16(r.header[16:18]) & 0x3fff)
	case "VP8L":
		bits := binary.LittleEndian.Uint32(r.header[9:13])
		width, height = int(bits&0x3fff)+1, int((bits>>14)&0x3fff)+1
	}
	if !thumbnailDimensions(width, height) {
		return 0, fail(415, "图片过大，无法生成缩略图")
	}
	if (chunk == "VP8 " || chunk == "VP8L") && r.canvasWidth != 0 && (width != r.canvasWidth || height != r.canvasHeight) {
		return 0, fail(415, "图片无法解码，文件可能已损坏或不是受支持的图片")
	}
	r.remaining = size + size%2 - probe
	r.pending = r.header[:8+probe]
	return r.Read(p)
}

func orientThumbnail(source *image.NRGBA, orientation int) *image.NRGBA {
	if orientation < 2 || orientation > 8 {
		return source
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	outWidth, outHeight := width, height
	if orientation >= 5 {
		outWidth, outHeight = height, width
	}
	out := image.NewNRGBA(image.Rect(0, 0, outWidth, outHeight))
	for y := range outHeight {
		for x := range outWidth {
			sx, sy := x, y
			switch orientation {
			case 2:
				sx = width - 1 - x
			case 3:
				sx, sy = width-1-x, height-1-y
			case 4:
				sy = height - 1 - y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, height-1-x
			case 7:
				sx, sy = width-1-y, height-1-x
			case 8:
				sx, sy = width-1-y, x
			}
			src := source.PixOffset(bounds.Min.X+sx, bounds.Min.Y+sy)
			dst := out.PixOffset(x, y)
			copy(out.Pix[dst:dst+4], source.Pix[src:src+4])
		}
	}
	return out
}

func thumbnailJPEGOrientation(reader io.Reader) int {
	r := bufio.NewReader(io.LimitReader(reader, 1<<20))
	var word [2]byte
	if _, err := io.ReadFull(r, word[:]); err != nil || word != [2]byte{0xff, 0xd8} {
		return 1
	}
	for {
		marker, err := r.ReadByte()
		if err != nil || marker != 0xff {
			return 1
		}
		for marker == 0xff {
			marker, err = r.ReadByte()
			if err != nil {
				return 1
			}
		}
		if marker == 0xda || marker == 0xd9 || marker == 0 {
			return 1
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if _, err = io.ReadFull(r, word[:]); err != nil {
			return 1
		}
		length := int(binary.BigEndian.Uint16(word[:])) - 2
		if length < 0 {
			return 1
		}
		if marker == 0xe1 && length >= 6 {
			var prefix [6]byte
			if _, err = io.ReadFull(r, prefix[:]); err != nil {
				return 1
			}
			length -= len(prefix)
			if string(prefix[:]) == "Exif\x00\x00" {
				// JPEG segment lengths are uint16: allocation is at most 65527.
				metadata := make([]byte, length)
				if _, err = io.ReadFull(r, metadata); err != nil {
					return 1
				}
				return thumbnailTIFFOrientation(metadata)
			}
		}
		if _, err = io.CopyN(io.Discard, r, int64(length)); err != nil {
			return 1
		}
	}
}

func thumbnailTIFFOrientation(data []byte) int {
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(data[2:4]) != 42 {
		return 1
	}
	offset := uint64(order.Uint32(data[4:8]))
	if offset < 8 || offset+2 > uint64(len(data)) {
		return 1
	}
	count := uint64(order.Uint16(data[offset : offset+2]))
	offset += 2
	if count > (uint64(len(data))-offset)/12 {
		return 1
	}
	for i := range count {
		entry := data[offset+i*12 : offset+(i+1)*12]
		if order.Uint16(entry[:2]) == 0x0112 && order.Uint16(entry[2:4]) == 3 && order.Uint32(entry[4:8]) == 1 {
			orientation := int(order.Uint16(entry[8:10]))
			if orientation >= 1 && orientation <= 8 {
				return orientation
			}
		}
	}
	return 1
}
