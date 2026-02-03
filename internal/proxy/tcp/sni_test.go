package tcp

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestMatchSNI(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
		patterns   []string
		expected   bool
	}{
		{
			name:       "exact match",
			serverName: "example.com",
			patterns:   []string{"example.com"},
			expected:   true,
		},
		{
			name:       "no match",
			serverName: "example.com",
			patterns:   []string{"other.com"},
			expected:   false,
		},
		{
			name:       "wildcard match",
			serverName: "api.example.com",
			patterns:   []string{"*.example.com"},
			expected:   true,
		},
		{
			name:       "wildcard no match - subdomain",
			serverName: "sub.api.example.com",
			patterns:   []string{"*.example.com"},
			expected:   false,
		},
		{
			name:       "wildcard no match - different domain",
			serverName: "api.other.com",
			patterns:   []string{"*.example.com"},
			expected:   false,
		},
		{
			name:       "multiple patterns - first match",
			serverName: "example.com",
			patterns:   []string{"example.com", "other.com"},
			expected:   true,
		},
		{
			name:       "multiple patterns - second match",
			serverName: "other.com",
			patterns:   []string{"example.com", "other.com"},
			expected:   true,
		},
		{
			name:       "empty patterns",
			serverName: "example.com",
			patterns:   []string{},
			expected:   false,
		},
		{
			name:       "empty server name",
			serverName: "",
			patterns:   []string{"example.com"},
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchSNI(tt.serverName, tt.patterns)
			if result != tt.expected {
				t.Errorf("MatchSNI(%q, %v) = %v, want %v", tt.serverName, tt.patterns, result, tt.expected)
			}
		})
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
		pattern    string
		expected   bool
	}{
		{"exact", "example.com", "example.com", true},
		{"wildcard single level", "api.example.com", "*.example.com", true},
		{"wildcard multiple levels fails", "a.b.example.com", "*.example.com", false},
		{"root domain no match wildcard", "example.com", "*.example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchPattern(tt.serverName, tt.pattern)
			if result != tt.expected {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.serverName, tt.pattern, result, tt.expected)
			}
		})
	}
}

func TestBufferedConn(t *testing.T) {
	// Create a pipe to simulate a connection
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// Write some data to the server side
	go func() {
		server.Write([]byte("Hello, World!"))
	}()

	// Create buffered conn on client side
	bc := NewBufferedConn(client)

	// Peek at first 5 bytes
	peeked, err := bc.Peek(5)
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if string(peeked) != "Hello" {
		t.Errorf("Peek got %q, want %q", string(peeked), "Hello")
	}

	// Read should return the same data
	buf := make([]byte, 5)
	n, err := bc.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 5 || string(buf) != "Hello" {
		t.Errorf("Read got %q (n=%d), want %q", string(buf[:n]), n, "Hello")
	}

	// Next read should get remaining data
	buf = make([]byte, 10)
	n, err = bc.Read(buf)
	if err != nil {
		t.Fatalf("Second Read failed: %v", err)
	}
	if string(buf[:n]) != ", World!" {
		t.Errorf("Second Read got %q, want %q", string(buf[:n]), ", World!")
	}
}

func TestParseTCPBackendURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"tcp://localhost:3306", "localhost:3306", false},
		{"tcp://mysql-server:3306", "mysql-server:3306", false},
		{"localhost:3306", "localhost:3306", false},
		{"192.168.1.1:5432", "192.168.1.1:5432", false},
		{"invalid", "", true},
		{"tcp://", "", false}, // url.Parse succeeds but Host is empty
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseTCPBackendURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseTCPBackendURL(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("parseTCPBackendURL(%q) unexpected error: %v", tt.input, err)
				return
			}
			if result != tt.expected {
				t.Errorf("parseTCPBackendURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestSNIExtraction tests SNI extraction from a real TLS ClientHello
func TestSNIExtraction(t *testing.T) {
	// A minimal TLS 1.2 ClientHello with SNI extension for "example.com"
	// This is a simplified version - real ClientHellos are more complex
	clientHello := buildTestClientHello("example.com")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// Write ClientHello to server side
	go func() {
		server.Write(clientHello)
	}()

	// Parse SNI from client side
	bc := NewBufferedConn(client)

	// Set a deadline to avoid hanging
	client.SetReadDeadline(time.Now().Add(1 * time.Second))

	sni, err := ParseClientHelloSNI(bc)
	if err != nil {
		t.Fatalf("ParseClientHelloSNI failed: %v", err)
	}
	if sni != "example.com" {
		t.Errorf("ParseClientHelloSNI got %q, want %q", sni, "example.com")
	}
}

// buildTestClientHello creates a minimal TLS ClientHello with SNI
func buildTestClientHello(serverName string) []byte {
	var buf bytes.Buffer

	// SNI extension
	sniExtension := buildSNIExtension(serverName)

	// Extensions length
	extensionsLen := len(sniExtension)

	// Handshake message (ClientHello)
	// Version (TLS 1.2)
	version := []byte{0x03, 0x03}
	// Random (32 bytes)
	random := make([]byte, 32)
	// Session ID (empty)
	sessionID := []byte{0x00}
	// Cipher suites (2 bytes length + one cipher)
	cipherSuites := []byte{0x00, 0x02, 0x00, 0x2f} // TLS_RSA_WITH_AES_128_CBC_SHA
	// Compression methods (1 byte length + null)
	compression := []byte{0x01, 0x00}

	// Calculate handshake length
	handshakePayload := bytes.Buffer{}
	handshakePayload.Write(version)
	handshakePayload.Write(random)
	handshakePayload.Write(sessionID)
	handshakePayload.Write(cipherSuites)
	handshakePayload.Write(compression)
	// Extensions length (2 bytes)
	handshakePayload.WriteByte(byte(extensionsLen >> 8))
	handshakePayload.WriteByte(byte(extensionsLen))
	handshakePayload.Write(sniExtension)

	// Handshake header
	handshakeLen := handshakePayload.Len()
	buf.WriteByte(0x01) // ClientHello
	buf.WriteByte(byte(handshakeLen >> 16))
	buf.WriteByte(byte(handshakeLen >> 8))
	buf.WriteByte(byte(handshakeLen))
	buf.Write(handshakePayload.Bytes())

	// TLS record header
	record := bytes.Buffer{}
	record.WriteByte(0x16) // Handshake
	record.Write([]byte{0x03, 0x01}) // TLS 1.0 for record layer
	recordLen := buf.Len()
	record.WriteByte(byte(recordLen >> 8))
	record.WriteByte(byte(recordLen))
	record.Write(buf.Bytes())

	return record.Bytes()
}

// buildSNIExtension creates an SNI extension
func buildSNIExtension(serverName string) []byte {
	nameBytes := []byte(serverName)
	nameLen := len(nameBytes)

	var buf bytes.Buffer
	// Extension type (0x0000 = server_name)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)

	// Extension data length
	extDataLen := 2 + 1 + 2 + nameLen // list length + name type + name length + name
	buf.WriteByte(byte(extDataLen >> 8))
	buf.WriteByte(byte(extDataLen))

	// Server Name list length
	listLen := 1 + 2 + nameLen
	buf.WriteByte(byte(listLen >> 8))
	buf.WriteByte(byte(listLen))

	// Name type (0 = host_name)
	buf.WriteByte(0x00)

	// Name length
	buf.WriteByte(byte(nameLen >> 8))
	buf.WriteByte(byte(nameLen))

	// Name
	buf.Write(nameBytes)

	return buf.Bytes()
}
