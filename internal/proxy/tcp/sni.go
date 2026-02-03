package tcp

import (
	"bytes"
	"errors"
	"io"
	"net"
)

// ErrNoSNI indicates no SNI was found in the TLS ClientHello
var ErrNoSNI = errors.New("no SNI found in ClientHello")

// ErrNotTLS indicates the connection does not appear to be TLS
var ErrNotTLS = errors.New("not a TLS connection")

// BufferedConn wraps a net.Conn to allow peeking without consuming bytes
type BufferedConn struct {
	net.Conn
	buffer *bytes.Buffer
}

// NewBufferedConn creates a new buffered connection
func NewBufferedConn(conn net.Conn) *BufferedConn {
	return &BufferedConn{
		Conn:   conn,
		buffer: new(bytes.Buffer),
	}
}

// Peek reads up to n bytes without consuming them from the connection
// Subsequent reads will return the peeked bytes first
func (bc *BufferedConn) Peek(n int) ([]byte, error) {
	// If we already have enough buffered, return from buffer
	if bc.buffer.Len() >= n {
		return bc.buffer.Bytes()[:n], nil
	}

	// Read more from the underlying connection
	buf := make([]byte, n-bc.buffer.Len())
	read, err := bc.Conn.Read(buf)
	if read > 0 {
		bc.buffer.Write(buf[:read])
	}
	if err != nil && err != io.EOF {
		return nil, err
	}

	if bc.buffer.Len() < n {
		return bc.buffer.Bytes(), io.ErrUnexpectedEOF
	}

	return bc.buffer.Bytes()[:n], nil
}

// Read implements io.Reader, first returning any buffered bytes
func (bc *BufferedConn) Read(b []byte) (int, error) {
	if bc.buffer.Len() > 0 {
		return bc.buffer.Read(b)
	}
	return bc.Conn.Read(b)
}

// ParseClientHelloSNI extracts the SNI from a TLS ClientHello message
// It peeks at the connection to read the handshake without consuming bytes
func ParseClientHelloSNI(conn *BufferedConn) (string, error) {
	// TLS record header is 5 bytes
	// Byte 0: Content type (0x16 = Handshake)
	// Bytes 1-2: Version
	// Bytes 3-4: Length of payload
	header, err := conn.Peek(5)
	if err != nil {
		return "", err
	}

	// Check for TLS handshake record type
	if header[0] != 0x16 {
		return "", ErrNotTLS
	}

	// Get the length of the handshake record
	recordLen := int(header[3])<<8 | int(header[4])
	if recordLen > 16384 { // Max TLS record size
		return "", ErrNotTLS
	}

	// Read the full ClientHello
	data, err := conn.Peek(5 + recordLen)
	if err != nil {
		return "", err
	}

	return extractSNI(data[5:])
}

// extractSNI parses the handshake message to find the SNI extension
func extractSNI(data []byte) (string, error) {
	if len(data) < 42 {
		return "", ErrNoSNI
	}

	// Handshake message type (should be 0x01 for ClientHello)
	if data[0] != 0x01 {
		return "", ErrNoSNI
	}

	// Skip: handshake type (1) + length (3) + version (2) + random (32)
	pos := 38

	// Session ID length
	if pos >= len(data) {
		return "", ErrNoSNI
	}
	sessionIDLen := int(data[pos])
	pos += 1 + sessionIDLen

	// Cipher suites length (2 bytes)
	if pos+2 > len(data) {
		return "", ErrNoSNI
	}
	cipherSuitesLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2 + cipherSuitesLen

	// Compression methods length (1 byte)
	if pos >= len(data) {
		return "", ErrNoSNI
	}
	compMethodsLen := int(data[pos])
	pos += 1 + compMethodsLen

	// Extensions length (2 bytes)
	if pos+2 > len(data) {
		return "", ErrNoSNI
	}
	extensionsLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2

	// Parse extensions
	extensionsEnd := pos + extensionsLen
	if extensionsEnd > len(data) {
		extensionsEnd = len(data)
	}

	for pos+4 <= extensionsEnd {
		extType := int(data[pos])<<8 | int(data[pos+1])
		extLen := int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4

		if pos+extLen > len(data) {
			break
		}

		// SNI extension type is 0x0000
		if extType == 0 {
			return parseSNIExtension(data[pos : pos+extLen])
		}

		pos += extLen
	}

	return "", ErrNoSNI
}

// parseSNIExtension parses the SNI extension data
func parseSNIExtension(data []byte) (string, error) {
	if len(data) < 5 {
		return "", ErrNoSNI
	}

	// SNI list length (2 bytes)
	listLen := int(data[0])<<8 | int(data[1])
	if listLen > len(data)-2 {
		return "", ErrNoSNI
	}

	pos := 2
	for pos+3 <= len(data) {
		nameType := data[pos]
		nameLen := int(data[pos+1])<<8 | int(data[pos+2])
		pos += 3

		if pos+nameLen > len(data) {
			return "", ErrNoSNI
		}

		// Name type 0 is host_name
		if nameType == 0 {
			return string(data[pos : pos+nameLen]), nil
		}

		pos += nameLen
	}

	return "", ErrNoSNI
}

// MatchSNI checks if a server name matches any of the patterns
// Patterns can be exact matches or wildcard patterns (*.example.com)
func MatchSNI(serverName string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchPattern(serverName, pattern) {
			return true
		}
	}
	return false
}

// matchPattern matches a server name against a pattern
// Supports wildcard patterns like *.example.com
func matchPattern(serverName, pattern string) bool {
	if pattern == serverName {
		return true
	}

	// Check for wildcard pattern
	if len(pattern) > 2 && pattern[0] == '*' && pattern[1] == '.' {
		suffix := pattern[1:] // .example.com
		// Server name must have at least one character before the suffix
		if len(serverName) > len(suffix) {
			sni := serverName[len(serverName)-len(suffix):]
			if sni == suffix {
				// Make sure there's no dot in the prefix (wildcard only matches one level)
				prefix := serverName[:len(serverName)-len(suffix)]
				for i := 0; i < len(prefix); i++ {
					if prefix[i] == '.' {
						return false
					}
				}
				return true
			}
		}
	}

	return false
}
