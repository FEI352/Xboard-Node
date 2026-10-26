package singbox

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/kernel/kerneltest"
	"github.com/cedar2025/xboard-node/internal/panel"
)

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
// (inbound remove + recreate on the same port), repeated UpdateUsers while
// clients keep connecting (in-place user swap racing with handshakes), and
// Stop (listeners released). Run it under -race to catch data races.
//
// It is opt-in because it binds real ports. Use the release build tags so
// QUIC protocols are available:
//
//	XBOARD_RUNTIME_TEST=1 go test -race -tags "with_quic with_utls" -run TestRuntimeLifecycle ./internal/kernel/singbox/
func TestRuntimeLifecycle(t *testing.T) {
	kerneltest.RequireRuntime(t)
	tlsCert := kerneltest.SelfSignedCert(t)
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
		{"hysteria", panel.NodeConfig{Protocol: "hysteria", Version: 1, UpMbps: 100, DownMbps: 100}, false},
		{"hysteria2", panel.NodeConfig{Protocol: "hysteria", Version: 2}, false},
		{"tuic", panel.NodeConfig{Protocol: "tuic"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := kerneltest.FreePort(t)
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

			// Keep handshaking against the inbound while users are swapped,
			// so -race sees user lookups racing with UpdateUsers.
			stopClients := make(chan struct{})
			clientsDone := make(chan struct{})
			go func() {
				defer close(clientsDone)
				hammer(port, tc.tcp, stopClients)
			}()
			for i := 0; i < 20; i++ {
				users := testUsers
				if i%2 == 0 {
					users = testUsers[:1]
				}
				if _, _, err := sb.UpdateUsers(users); err != nil {
					close(stopClients)
					<-clientsDone
					t.Fatalf("update users #%d: %v", i, err)
				}
				time.Sleep(5 * time.Millisecond)
			}
			close(stopClients)
			<-clientsDone
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

// hammer opens short-lived connections that send a bogus handshake until stop
// is closed. Each one makes the inbound look up its current user table.
func hammer(port int, tcp bool, stop <-chan struct{}) {
	network := "udp"
	if tcp {
		network = "tcp"
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	payload := make([]byte, 128)
	for {
		select {
		case <-stop:
			return
		default:
		}
		_, _ = rand.Read(payload)
		conn, err := net.DialTimeout(network, addr, 200*time.Millisecond)
		if err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(50 * time.Millisecond))
		_, _ = conn.Write(payload)
		_, _ = conn.Read(payload)
		_ = conn.Close()
	}
}
