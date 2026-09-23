package email

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-smtp"

	"notifyrelay/internal/channel"
)

// Both encryption modes are exercised against a real TLS listener, because
// "port 465 with STARTTLS" style mistakes only surface when a handshake
// actually happens.

// selfSignedCert returns a certificate valid for 127.0.0.1 plus the path of a
// PEM file holding it, so the channel can be pointed at it via ca_file.
func selfSignedCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatalf("write ca file: %v", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("key pair: %v", err)
	}
	return cert, caPath
}

func splitAddr(t *testing.T, addr net.Addr) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return host, port
}

// STARTTLS, the port-587 style: connect in the clear, then upgrade.
func TestSend_StartTLS(t *testing.T) {
	cert, caPath := selfSignedCert(t)
	be := newFakeBackend()

	srv := smtp.NewServer(be)
	srv.Domain = "test.local"
	srv.AllowInsecureAuth = true
	srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	host, port := splitAddr(t, ln.Addr())

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "starttls",
		"from": "relay@example.com", "to": []any{"ops@example.com"},
		"ca_file": caPath,
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{})
	if res.Class != channel.ClassSent {
		t.Fatalf("STARTTLS send = %v (%v, %s)", res.Class, res.Err, res.Detail)
	}
	if got := be.captured(); len(got) != 1 {
		t.Fatalf("server received %d messages, want 1", len(got))
	}
}

// Implicit TLS, the port-465 style: the socket is TLS from the first byte.
func TestSend_ImplicitTLS(t *testing.T) {
	cert, caPath := selfSignedCert(t)
	be := newFakeBackend()

	srv := smtp.NewServer(be)
	srv.Domain = "test.local"
	srv.AllowInsecureAuth = true
	srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", srv.TLSConfig)
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	host, port := splitAddr(t, ln.Addr())

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "implicit",
		"from": "relay@example.com", "to": []any{"ops@example.com"},
		"ca_file": caPath,
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{})
	if res.Class != channel.ClassSent {
		t.Fatalf("implicit TLS send = %v (%v, %s)", res.Class, res.Err, res.Detail)
	}
	if got := be.captured(); len(got) != 1 {
		t.Fatalf("server received %d messages, want 1", len(got))
	}
}

// An untrusted certificate must be refused, and refused as CONNECT_ERROR:
// no peer was ever reached, so it must not consume the channel's send quota.
func TestSend_UntrustedCertificateIsRefused(t *testing.T) {
	cert, _ := selfSignedCert(t)
	be := newFakeBackend()

	srv := smtp.NewServer(be)
	srv.Domain = "test.local"
	srv.AllowInsecureAuth = true
	srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	host, port := splitAddr(t, ln.Addr())

	// No ca_file, so the self-signed certificate is unknown to the client.
	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "starttls",
		"from": "relay@example.com", "to": []any{"ops@example.com"},
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{})
	if res.Class != channel.ClassConnectError {
		t.Fatalf("class = %v (%v, %s), want CONNECT_ERROR", res.Class, res.Err, res.Detail)
	}
	if len(be.captured()) != 0 {
		t.Error("a message was delivered over an untrusted connection")
	}
}

func TestNew_RejectsABadCAFile(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "missing file", path: filepath.Join(t.TempDir(), "nope.pem")},
		{name: "no PEM content", path: writeTemp(t, "not a certificate")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New("test", map[string]any{
				"host": "smtp.example.com", "from": "a@b.c", "to": []any{"x@y.z"},
				"ca_file": tt.path,
			})
			if err == nil {
				t.Fatal("expected an error: a bad CA file must fail at startup, not at delivery time")
			}
		})
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file.pem")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
