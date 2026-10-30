package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const minimumCompressionBytes = 1024

var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

// Compress only complete text representations; downloads and streaming protocols keep their framing.
func compressResponse(request *http.Request, writer *captureWriter, data []byte) []byte {
	headers := writer.Header()
	if request.Method != http.MethodGet || headers.Get("Content-Encoding") != "" ||
		hasDirective(headers.Get("Cache-Control"), "no-transform") {
		return data
	}
	if writer.Code == http.StatusNotModified {
		varyAcceptEncoding(headers)
		return data
	}
	if writer.Code != http.StatusOK || len(data) < minimumCompressionBytes ||
		request.Header.Get("Range") != "" || headers.Get("Content-Range") != "" ||
		!compressibleContentType(headers.Get("Content-Type")) {
		return data
	}
	varyAcceptEncoding(headers)
	if !acceptsGzip(strings.Join(request.Header.Values("Accept-Encoding"), ",")) {
		return data
	}
	compressed := gzipBytes(data)
	if len(compressed) >= len(data) {
		return data
	}
	headers.Set("Content-Encoding", "gzip")
	headers.Set("Content-Length", strconv.Itoa(len(compressed)))
	return compressed
}

func gzipBytes(data []byte) []byte {
	var output bytes.Buffer
	writer := gzipWriters.Get().(*gzip.Writer)
	writer.Reset(&output)
	defer func() {
		writer.Reset(io.Discard)
		gzipWriters.Put(writer)
	}()
	if _, err := writer.Write(data); err != nil {
		return data
	}
	if err := writer.Close(); err != nil {
		return data
	}
	return output.Bytes()
}

func compressibleContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || mediaType == "text/event-stream" {
		return false
	}
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/javascript", "application/x-javascript",
		"application/xml", "application/manifest+json", "image/svg+xml":
		return true
	}
	return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
}

func hasDirective(value, directive string) bool {
	for _, part := range strings.Split(value, ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		if strings.EqualFold(name, directive) {
			return true
		}
	}
	return false
}

func varyAcceptEncoding(headers http.Header) {
	values := strings.Join(headers.Values("Vary"), ", ")
	if hasDirective(values, "*") || hasDirective(values, "Accept-Encoding") {
		return
	}
	if values != "" {
		values += ", "
	}
	headers.Set("Vary", values+"Accept-Encoding")
}

func acceptsGzip(value string) bool {
	qualities := map[string]float64{}
	for _, part := range strings.Split(value, ",") {
		parts := strings.Split(part, ";")
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		qualities[name] = encodingQuality(parts[1:])
	}
	quality, explicit := qualities["gzip"]
	if !explicit {
		quality = qualities["*"]
	}
	return quality > 0 && quality >= qualities["identity"]
}

func encodingQuality(parameters []string) float64 {
	quality := 1.0
	for _, parameter := range parameters {
		name, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
		if !found || !strings.EqualFold(name, "q") {
			return 0
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || !(parsed >= 0 && parsed <= 1) {
			return 0
		}
		quality = parsed
	}
	return quality
}
