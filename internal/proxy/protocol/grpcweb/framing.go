package grpcweb

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const (
	// flagData indicates a data frame.
	flagData byte = 0x00
	// flagTrailer indicates a trailer frame.
	flagTrailer byte = 0x80

	// defaultMaxMessageSize is the default maximum message size (4MB).
	defaultMaxMessageSize = 4 * 1024 * 1024

	// frameHeaderSize is the size of a gRPC-Web frame header (1 flag + 4 length).
	frameHeaderSize = 5
)

// grpcWebFrame represents a decoded gRPC-Web frame.
type grpcWebFrame struct {
	Flag    byte
	Payload []byte
}

// isTrailer returns true if this is a trailer frame.
func (f *grpcWebFrame) isTrailer() bool {
	return f.Flag&flagTrailer != 0
}

// decodeGRPCWebFrame reads a single gRPC-Web frame from the reader.
// Frame format: 1 byte flag | 4 bytes length (big-endian) | payload.
func decodeGRPCWebFrame(r io.Reader, maxSize int) (*grpcWebFrame, error) {
	header := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	flag := header[0]
	length := binary.BigEndian.Uint32(header[1:5])

	if maxSize > 0 && int(length) > maxSize {
		return nil, fmt.Errorf("message size %d exceeds maximum %d", length, maxSize)
	}

	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
	}

	return &grpcWebFrame{Flag: flag, Payload: payload}, nil
}

// encodeDataFrame encodes a data payload as a gRPC-Web data frame.
func encodeDataFrame(data []byte) []byte {
	frame := make([]byte, frameHeaderSize+len(data))
	frame[0] = flagData
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(data)))
	copy(frame[frameHeaderSize:], data)
	return frame
}

// encodeTrailerFrame encodes trailers as a gRPC-Web trailer frame.
// Trailers are encoded as "key: value\r\n" pairs.
func encodeTrailerFrame(trailers map[string]string) []byte {
	var sb strings.Builder
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(trailers))
	for k := range trailers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(": ")
		sb.WriteString(trailers[k])
		sb.WriteString("\r\n")
	}
	payload := []byte(sb.String())

	frame := make([]byte, frameHeaderSize+len(payload))
	frame[0] = flagTrailer
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[frameHeaderSize:], payload)
	return frame
}

// parseTrailerFrame parses a trailer frame payload into key-value pairs.
func parseTrailerFrame(payload []byte) map[string]string {
	trailers := make(map[string]string)
	for _, line := range strings.Split(string(payload), "\r\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) == 2 {
			trailers[parts[0]] = parts[1]
		}
	}
	return trailers
}

// isGRPCWebRequest returns true if the request has a gRPC-Web content type.
func isGRPCWebRequest(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return isGRPCWebContentType(ct)
}

// isGRPCWebContentType returns true if the content type is a gRPC-Web type.
func isGRPCWebContentType(ct string) bool {
	// Trim parameters (e.g., charset).
	if idx := strings.IndexByte(ct, ';'); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}
	switch ct {
	case "application/grpc-web",
		"application/grpc-web+proto",
		"application/grpc-web-text",
		"application/grpc-web-text+proto":
		return true
	}
	return false
}

// isTextMode returns true if the content type indicates base64-encoded text mode.
func isTextMode(contentType string) bool {
	if idx := strings.IndexByte(contentType, ';'); idx != -1 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	return contentType == "application/grpc-web-text" ||
		contentType == "application/grpc-web-text+proto"
}

// base64Encode encodes data as standard base64.
func base64Encode(data []byte) []byte {
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
	base64.StdEncoding.Encode(encoded, data)
	return encoded
}

// base64Decode decodes standard base64 data.
func base64Decode(data []byte) ([]byte, error) {
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(data)))
	n, err := base64.StdEncoding.Decode(decoded, data)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}
	return decoded[:n], nil
}
