package kerneltest

import (
	"net"
	"testing"

	singM "github.com/sagernet/sing/common/metadata"
)

// SSRFCase describes one kernel/protocol combination to check.
type SSRFCase struct {
	// Start runs a node on port and returns a stop function. With
	// allowLoopback the kernel must be configured to deliberately permit
	// loopback destinations; that run is the positive control proving the
	// client, protocol and test services work at all.
	Start func(t *testing.T, port int, allowLoopback bool) (stop func())
	// Client returns a sing-box outbound connecting to the node on port.
	Client func(port int) map[string]any
	// UDP enables the UDP checks (the protocol must carry UDP).
	UDP bool
}

// UDPProbeFirst is the public destination a UDP session is opened with before
// trying to smuggle a packet to loopback (UDP discard port; harmless).
var UDPProbeFirst = singM.ParseSocksaddrHostPort("1.1.1.1", 9)

func hasIPv6Loopback() bool {
	l, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// AssertLoopbackBlocked checks that a proxy user cannot reach services bound
// to the node's loopback interface: by IP, by a domain resolving to it
// ("localhost"), via 0.0.0.0 / IPv4-mapped IPv6, or by UDP — either directly
// or smuggled inside a UDP session first routed to a public address.
func AssertLoopbackBlocked(t *testing.T, c SSRFCase) {
	t.Helper()
	tcpEcho := StartEcho(t)
	udpEcho := StartUDPEcho(t)
	blockedHosts := []string{"127.0.0.1", "localhost", "0.0.0.0", "::ffff:127.0.0.1"}
	if hasIPv6Loopback() {
		blockedHosts = append(blockedHosts, "::1")
	}
	udpTarget := singM.ParseSocksaddrHostPort("127.0.0.1", uint16(udpEcho))

	t.Run("allowed-control", func(t *testing.T) {
		port := FreePort(t)
		defer c.Start(t, port, true)()
		for _, host := range []string{"127.0.0.1", "localhost"} {
			if err := RoundTrip(c.Client(port), host, tcpEcho); err != nil {
				t.Errorf("control: TCP via %q failed: %v", host, err)
			}
		}
		if c.UDP {
			if ok, err := UDPProbe(c.Client(port), UDPProbeFirst, udpTarget); err != nil || !ok {
				t.Errorf("control: UDP to loopback failed: ok=%v err=%v", ok, err)
			}
		}
	})

	t.Run("blocked", func(t *testing.T) {
		port := FreePort(t)
		defer c.Start(t, port, false)()
		for _, host := range blockedHosts {
			if err := RoundTrip(c.Client(port), host, tcpEcho); err == nil {
				t.Errorf("TCP reached loopback echo via %q: SSRF protection bypassed", host)
			}
		}
		if c.UDP {
			if ok, _ := UDPProbe(c.Client(port), udpTarget, udpTarget); ok {
				t.Errorf("UDP reached loopback echo directly")
			}
			if ok, _ := UDPProbe(c.Client(port), UDPProbeFirst, udpTarget); ok {
				t.Errorf("UDP reached loopback echo smuggled after a public destination")
			}
		}
	})
}
