package filesharing

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

var asciiExtension = regexp.MustCompile(`^\.[a-zA-Z0-9]{1,16}$`)

func attachmentDisposition(name string) string {
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	extended, needsFallback := strings.CutPrefix(disposition, "attachment; filename*=")
	if !needsFallback {
		return disposition
	}
	fallback := "download"
	if extension := path.Ext(name); asciiExtension.MatchString(extension) {
		fallback += extension
	}
	// RFC 6266 specifies the ASCII fallback before the UTF-8 filename.
	return mime.FormatMediaType("attachment", map[string]string{"filename": fallback}) + "; filename*=" + extended
}

func (s service) download(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	item, err := s.store.get(c.Request.Context(), c.Param("id"))
	if err != nil {
		respond(c, nil, err)
		return
	}
	if item.UploadedBytes != item.Size {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Content-Disposition", attachmentDisposition(item.Name))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("X-Content-Type-Options", "nosniff")
	// A rename must preserve the validator used by a browser resuming the immutable bytes.
	c.Header("ETag", `"`+item.ID+`"`)
	c.Set("skipETag", true)
	reader := &downloadReader{size: item.Size, index: -1, load: func(index int) ([]byte, error) {
		var part chunk
		err := s.store.db.WithContext(c.Request.Context()).Select("data").
			Take(&part, "file_id = ? AND chunk_index = ?", item.ID, index).Error
		return part.Data, err
	}}
	http.ServeContent(c.Writer, c.Request, item.Name, item.CreatedAt, reader)
	if reader.readError != nil && c.Request.Context().Err() == nil {
		slog.Warn("shared file download interrupted", "error", reader.readError)
	}
}

// ServeContent handles the HTTP range protocol. Load only the chunks it reads and release each
// database connection before sending bytes to the client.
type downloadReader struct {
	size, offset int64
	index        int
	data         []byte
	load         func(int) ([]byte, error)
	readError    error
}

func (r *downloadReader) Read(buffer []byte) (int, error) {
	if r.offset >= r.size {
		return 0, io.EOF
	}
	index := int(r.offset / chunkBytes)
	if index != r.index {
		data, err := r.load(index)
		if err != nil {
			r.readError = err
			return 0, err
		}
		if int64(len(data)) != min(chunkBytes, r.size-int64(index)*chunkBytes) {
			r.readError = io.ErrUnexpectedEOF
			return 0, r.readError
		}
		r.index, r.data = index, data
	}
	n := copy(buffer, r.data[r.offset%chunkBytes:])
	r.offset += int64(n)
	return n, nil
}

func (r *downloadReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.offset
	case io.SeekEnd:
		offset += r.size
	default:
		return 0, errors.New("invalid seek origin")
	}
	if offset < 0 {
		return 0, errors.New("negative file offset")
	}
	r.offset = offset
	return offset, nil
}
