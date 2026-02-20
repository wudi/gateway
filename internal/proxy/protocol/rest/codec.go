package rest

import (
	"encoding/binary"
	"fmt"
	"io"
)

// gRPC wire format:
// 1 byte: compressed flag (0 = uncompressed, 1 = compressed)
// 4 bytes: message length (big-endian uint32)
// N bytes: message data

// decodeGRPCFrame reads a single gRPC length-prefixed frame from the reader.
// Returns the raw message bytes (protobuf or JSON depending on content-type).
func decodeGRPCFrame(r io.Reader) ([]byte, bool, error) {
	// Read 5-byte header
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, false, io.EOF
		}
		return nil, false, fmt.Errorf("reading grpc frame header: %w", err)
	}

	compressed := header[0] == 1
	length := binary.BigEndian.Uint32(header[1:5])

	if length > 64*1024*1024 { // 64 MB sanity limit
		return nil, false, fmt.Errorf("grpc frame too large: %d bytes", length)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, false, fmt.Errorf("reading grpc frame body: %w", err)
	}

	return data, compressed, nil
}

// encodeGRPCFrame writes a gRPC length-prefixed frame.
func encodeGRPCFrame(w io.Writer, data []byte, compressed bool) error {
	header := make([]byte, 5)
	if compressed {
		header[0] = 1
	}
	binary.BigEndian.PutUint32(header[1:5], uint32(len(data)))

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("writing grpc frame header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("writing grpc frame body: %w", err)
	}
	return nil
}
