package singbox

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/kernel"
	"github.com/cedar2025/xboard-node/internal/panel"
)

// selfSignedCert returns a throwaway certificate for TLS-based protocols.
func selfSignedCert(t *testing.T) kernel.TLSCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return kernel.TLSCert{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func tcpListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func waitTCP(t *testing.T, port int, want bool, step string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tcpListening(port) == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: port %d listening=%v, want %v", step, port, !want, want)
}

// TestRuntimeLifecycle starts a real sing-box instance for each protocol and
// exercises the hot paths xboard-node relies on: Reload with a changed config
// (inbound remove + recreate on the same port), UpdateUsers (in-place user
// swap) and Stop (listeners released).
//
// It is opt-in because it binds real ports and, under -race, trips on known
// data races in the in-place UpdateUsers paths (vmess/socks/http). Run with
// the release build tags so QUIC protocols are available:
//
//	XBOARD_RUNTIME_TEST=1 go test -tags "with_quic with_utls" -run TestRuntimeLifecycle ./internal/kernel/singbox/
func TestRuntimeLifecycle(t *testing.T) {
	if os.Getenv("XBOARD_RUNTIME_TEST") == "" {
		t.Skip("set XBOARD_RUNTIME_TEST=1 to run (starts real listeners)")
	}
	tlsCert := selfSignedCert(t)
	ss2022Key := base64.StdEncoding.EncodeToString(make([]byte, 16))

	cases := []struct {
		name string
		nc   panel.NodeConfig
		tcp  bool
	}{
		{"shadowsocks", panel.NodeConfig{Protocol: "shadowsocks", Cipher: "aes-128-gcm"}, true},
		{"shadowsocks2022", panel.NodeConfig{Protocol: "shadowsocks", Cipher: "2022-blake3-aes-128-gcm", ServerKey: ss2022Key}, true},
		{"vmess", panel.NodeConfig{Protocol: "vmess"}, true},
		{"vless", panel.NodeConfig{Protocol: "vless"}, true},
		{"trojan", panel.NodeConfig{Protocol: "trojan"}, true},
		{"anytls", panel.NodeConfig{Protocol: "anytls"}, true},
		{"naive", panel.NodeConfig{Protocol: "naive", TLS: 1}, true},
		{"socks", panel.NodeConfig{Protocol: "socks"}, true},
		{"http", panel.NodeConfig{Protocol: "http"}, true},
		{"mieru", panel.NodeConfig{Protocol: "mieru", Transport: "TCP"}, true},
		{"hysteria2", panel.NodeConfig{Protocol: "hysteria", Version: 2}, false},
		{"tuic", panel.NodeConfig{Protocol: "tuic"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := freePort(t)
			nc := tc.nc
			nc.ServerPort = port
			spec := testNodeSpec(&nc)

			sb := New(config.KernelConfig{LogLevel: "error", ConfigDir: t.TempDir()})
			if err := sb.Start(spec, testUsers[:1], tlsCert); err != nil {
				t.Fatalf("start: %v", err)
			}
			stopped := false
			defer func() {
				if !stopped {
					sb.Stop()
				}
			}()
			if tc.tcp {
				waitTCP(t, port, true, "after start")
			}

			// Different user set changes the config hash, forcing the
			// inbound to be removed and recreated on the same port.
			if err := sb.Reload(spec, testUsers, tlsCert); err != nil {
				t.Fatalf("reload: %v", err)
			}
			if tc.tcp {
				waitTCP(t, port, true, "after reload")
			}

			if _, _, err := sb.UpdateUsers(testUsers[:1]); err != nil {
				t.Fatalf("update users: %v", err)
			}
			if tc.tcp {
				waitTCP(t, port, true, "after update users")
			}

			sb.Stop()
			stopped = true
			if tc.tcp {
				waitTCP(t, port, false, "after stop")
			}
		})
	}
}
