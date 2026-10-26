package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	singJSON "github.com/sagernet/sing/common/json"
	singM "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/model"
	"github.com/cedar2025/xboard-node/internal/panel"
)

// startEcho runs a TCP echo server on 127.0.0.1 and returns its port.
func startEcho(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

// proxyRoundTrip starts a fresh sing-box client (so every call performs a new
// QUIC handshake and authentication), sends a payload through the outbound to
// the echo server and checks it comes back unchanged.
func proxyRoundTrip(outbound map[string]any, echoPort int) error {
	outbound["tag"] = "proxy"
	data, err := json.Marshal(map[string]any{
		"log":       map[string]any{"level": "panic"},
		"outbounds": []any{outbound},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	defer cancel()
	opts, err := singJSON.UnmarshalExtendedContext[option.Options](ctx, data)
	if err != nil {
		return fmt.Errorf("client options: %w", err)
	}
	instance, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		return fmt.Errorf("client create: %w", err)
	}
	defer instance.Close()
	if err = instance.Start(); err != nil {
		return fmt.Errorf("client start: %w", err)
	}
	out, ok := service.FromContext[adapter.OutboundManager](ctx).Outbound("proxy")
	if !ok {
		return fmt.Errorf("client outbound missing")
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 3*time.Second)
	defer dialCancel()
	conn, err := out.DialContext(dialCtx, N.NetworkTCP, singM.ParseSocksaddrHostPort("127.0.0.1", uint16(echoPort)))
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("xboard-e2e-" + time.Now().String())
	if _, err = conn.Write(payload); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	reply := make([]byte, len(payload))
	if _, err = io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if !bytes.Equal(reply, payload) {
		return fmt.Errorf("echo mismatch")
	}
	return nil
}

// TestE2EQUICUserSwap connects real sing-box clients to the QUIC protocols
// while xboard-node swaps the user list, under -race. It checks that:
//
//   - a user present in every list keeps authenticating during the churn
//     (hysteria2/tuic swap users in place via the sing-quic fork),
//
//   - a removed user is rejected and a re-added one accepted afterwards, i.e.
//     updates actually take effect (hysteria v1 is recreated instead).
//
//     XBOARD_RUNTIME_TEST=1 go test -race -tags "with_quic with_utls" -run TestE2EQUICUserSwap ./internal/kernel/singbox/
func TestE2EQUICUserSwap(t *testing.T) {
	if os.Getenv("XBOARD_RUNTIME_TEST") == "" {
		t.Skip("set XBOARD_RUNTIME_TEST=1 to run (starts real listeners)")
	}
	tlsCert := selfSignedCert(t)
	echoPort := startEcho(t)
	stable, other := testUsers[0], testUsers[1]
	tlsClient := func() map[string]any {
		return map[string]any{"enabled": true, "insecure": true, "server_name": "localhost", "alpn": []string{"h3"}}
	}

	cases := []struct {
		name    string
		nc      panel.NodeConfig
		inPlace bool
		client  func(port int, u model.UserSpec) map[string]any
	}{
		{"hysteria2", panel.NodeConfig{Protocol: "hysteria", Version: 2}, true, func(port int, u model.UserSpec) map[string]any {
			return map[string]any{"type": "hysteria2", "server": "127.0.0.1", "server_port": port, "password": u.UUID, "tls": tlsClient()}
		}},
		{"tuic", panel.NodeConfig{Protocol: "tuic"}, true, func(port int, u model.UserSpec) map[string]any {
			return map[string]any{"type": "tuic", "server": "127.0.0.1", "server_port": port, "uuid": u.UUID, "password": u.UUID, "tls": tlsClient()}
		}},
		{"hysteria", panel.NodeConfig{Protocol: "hysteria", Version: 1, UpMbps: 100, DownMbps: 100}, false, func(port int, u model.UserSpec) map[string]any {
			return map[string]any{"type": "hysteria", "server": "127.0.0.1", "server_port": port, "auth_str": u.UUID, "up_mbps": 100, "down_mbps": 100, "tls": tlsClient()}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := freePort(t)
			nc := tc.nc
			nc.ServerPort = port
			spec := testNodeSpec(&nc)
			sb := New(config.KernelConfig{
				LogLevel:  "error",
				ConfigDir: t.TempDir(),
				// Let the test reach the loopback echo server past the SSRF block.
				CustomRoute: []map[string]any{{"ip_cidr": []string{"127.0.0.1/32"}, "port": []int{echoPort}, "outbound": "direct"}},
			})
			if err := sb.Start(spec, []model.UserSpec{stable}, tlsCert); err != nil {
				t.Fatalf("start: %v", err)
			}
			defer sb.Stop()

			if err := proxyRoundTrip(tc.client(port, stable), echoPort); err != nil {
				t.Fatalf("initial user: %v", err)
			}
			if err := proxyRoundTrip(tc.client(port, other), echoPort); err == nil {
				t.Fatalf("user not yet added was accepted")
			}

			if tc.inPlace {
				// Handshake continuously while users are swapped in place.
				stop := make(chan struct{})
				done := make(chan struct{})
				var ok, failed atomic.Int32
				var lastErr atomic.Value
				go func() {
					defer close(done)
					for {
						select {
						case <-stop:
							return
						default:
						}
						if err := proxyRoundTrip(tc.client(port, stable), echoPort); err != nil {
							failed.Add(1)
							lastErr.Store(err.Error())
						} else {
							ok.Add(1)
						}
					}
				}()
				for i := 0; i < 30; i++ {
					users := []model.UserSpec{stable, other}
					if i%2 == 0 {
						users = []model.UserSpec{stable}
					}
					if _, _, err := sb.UpdateUsers(users); err != nil {
						t.Fatalf("update users #%d: %v", i, err)
					}
					time.Sleep(10 * time.Millisecond)
				}
				close(stop)
				<-done
				if failed.Load() > 0 || ok.Load() == 0 {
					t.Fatalf("stable user during swaps: %d ok, %d failed (last: %v)", ok.Load(), failed.Load(), lastErr.Load())
				}
				t.Logf("stable user during swaps: %d handshakes ok", ok.Load())
			}

			// Updates must take effect: swap to only `other`.
			if _, _, err := sb.UpdateUsers([]model.UserSpec{other}); err != nil {
				t.Fatalf("update users: %v", err)
			}
			if err := proxyRoundTrip(tc.client(port, other), echoPort); err != nil {
				t.Fatalf("added user rejected: %v", err)
			}
			if err := proxyRoundTrip(tc.client(port, stable), echoPort); err == nil {
				t.Fatalf("removed user still accepted")
			}
		})
	}
}
