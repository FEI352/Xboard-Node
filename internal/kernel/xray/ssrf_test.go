package xray

import (
	"testing"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/kernel/kerneltest"
	"github.com/cedar2025/xboard-node/internal/model"
	"github.com/cedar2025/xboard-node/internal/panel"
)

var loopbackCIDRs = []string{"127.0.0.1/32", "::1/128"}

// TestE2ELoopbackBlocked drives real clients through the xray kernel and
// checks that users cannot reach the node's loopback (SSRF).
//
//	XBOARD_RUNTIME_TEST=1 go test -run TestE2ELoopbackBlocked ./internal/kernel/xray/
func TestE2ELoopbackBlocked(t *testing.T) {
	kerneltest.RequireRuntime(t)
	user := testUsers[0]
	for _, tc := range []struct {
		name   string
		nc     panel.NodeConfig
		client func(port int) map[string]any
	}{
		{"shadowsocks", panel.NodeConfig{Protocol: "shadowsocks", Cipher: "aes-128-gcm"}, func(port int) map[string]any {
			return map[string]any{"type": "shadowsocks", "server": "127.0.0.1", "server_port": port, "method": "aes-128-gcm", "password": user.UUID}
		}},
		// socks/http inbounds get no default private-IP block from xray itself.
		{"socks", panel.NodeConfig{Protocol: "socks"}, func(port int) map[string]any {
			return map[string]any{"type": "socks", "server": "127.0.0.1", "server_port": port, "username": user.UUID, "password": user.UUID}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kerneltest.AssertLoopbackBlocked(t, kerneltest.SSRFCase{
				UDP:    true,
				Client: tc.client,
				Start: func(t *testing.T, port int, allowLoopback bool) func() {
					cfg := config.KernelConfig{Type: "xray", LogLevel: "error", ConfigDir: t.TempDir()}
					nc := tc.nc
					nc.ServerPort = port
					spec := testNodeSpec(&nc)
					if allowLoopback {
						// Deliberate opt-out: own "direct" without the block + allow route.
						cfg.CustomOutbound = []map[string]any{{"protocol": "freedom", "tag": "direct", "settings": map[string]any{
							"finalRules": []any{map[string]any{"action": "allow", "ip": loopbackCIDRs}},
						}}}
						spec.CustomRouteRules = append(spec.CustomRouteRules, model.CustomRouteRule{
							Name:   "allow-loopback",
							Match:  model.RouteMatch{IPCIDRs: loopbackCIDRs},
							Action: model.RouteAction{Type: "direct"},
						})
					}
					x := New(cfg)
					if err := x.Start(spec, []model.UserSpec{user}, kerneltest.SelfSignedCert(t)); err != nil {
						t.Fatalf("start: %v", err)
					}
					return x.Stop
				},
			})
		})
	}
}
