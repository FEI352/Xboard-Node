package singbox

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/kernel/kerneltest"
	"github.com/cedar2025/xboard-node/internal/model"
	"github.com/cedar2025/xboard-node/internal/panel"
)

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
	kerneltest.RequireRuntime(t)
	tlsCert := kerneltest.SelfSignedCert(t)
	echoPort := kerneltest.StartEcho(t)
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
			port := kerneltest.FreePort(t)
			nc := tc.nc
			nc.ServerPort = port
			spec := testNodeSpec(&nc)
			// Deliberately let the test reach the loopback echo server past
			// the SSRF protection.
			spec.CustomRouteRules = append(spec.CustomRouteRules, allowLoopbackRule())
			sb := New(config.KernelConfig{LogLevel: "error", ConfigDir: t.TempDir(), CustomOutbound: unguardedDirect})
			if err := sb.Start(spec, []model.UserSpec{stable}, tlsCert); err != nil {
				t.Fatalf("start: %v", err)
			}
			defer sb.Stop()

			if err := kerneltest.RoundTrip(tc.client(port, stable), "127.0.0.1", echoPort); err != nil {
				t.Fatalf("initial user: %v", err)
			}
			if err := kerneltest.RoundTrip(tc.client(port, other), "127.0.0.1", echoPort); err == nil {
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
						if err := kerneltest.RoundTrip(tc.client(port, stable), "127.0.0.1", echoPort); err != nil {
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
			if err := kerneltest.RoundTrip(tc.client(port, other), "127.0.0.1", echoPort); err != nil {
				t.Fatalf("added user rejected: %v", err)
			}
			if err := kerneltest.RoundTrip(tc.client(port, stable), "127.0.0.1", echoPort); err == nil {
				t.Fatalf("removed user still accepted")
			}
		})
	}
}
