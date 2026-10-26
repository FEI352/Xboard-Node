package singbox

import (
	"testing"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/kernel/kerneltest"
	"github.com/cedar2025/xboard-node/internal/model"
	"github.com/cedar2025/xboard-node/internal/panel"
)

var loopbackCIDRs = []string{"127.0.0.1/32", "::1/128"}

// unguardedDirect replaces the default direct outbound (which carries
// block_destinations) for tests that deliberately talk to loopback services.
var unguardedDirect = []map[string]any{{"type": "direct", "tag": "direct"}}

func allowLoopbackRule() model.CustomRouteRule {
	return model.CustomRouteRule{
		Name:   "allow-loopback",
		Match:  model.RouteMatch{IPCIDRs: loopbackCIDRs},
		Action: model.RouteAction{Type: "direct"},
	}
}

// TestE2ELoopbackBlocked drives real clients through the sing-box kernel and
// checks that users cannot reach the node's loopback (SSRF).
//
//	XBOARD_RUNTIME_TEST=1 go test -run TestE2ELoopbackBlocked ./internal/kernel/singbox/
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
		{"socks", panel.NodeConfig{Protocol: "socks"}, func(port int) map[string]any {
			return map[string]any{"type": "socks", "server": "127.0.0.1", "server_port": port, "username": user.UUID, "password": user.UUID}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kerneltest.AssertLoopbackBlocked(t, kerneltest.SSRFCase{
				UDP:    true,
				Client: tc.client,
				Start: func(t *testing.T, port int, allowLoopback bool) func() {
					cfg := config.KernelConfig{LogLevel: "error", ConfigDir: t.TempDir()}
					nc := tc.nc
					nc.ServerPort = port
					spec := testNodeSpec(&nc)
					if allowLoopback {
						cfg.CustomOutbound = unguardedDirect
						spec.CustomRouteRules = append(spec.CustomRouteRules, allowLoopbackRule())
					}
					sb := New(cfg)
					if err := sb.Start(spec, []model.UserSpec{user}, kerneltest.SelfSignedCert(t)); err != nil {
						t.Fatalf("start: %v", err)
					}
					return sb.Stop
				},
			})
		})
	}
}
