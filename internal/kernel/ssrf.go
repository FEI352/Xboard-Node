package kernel

// BlockedDestinationCIDRs are destinations proxy users must never reach
// through the node: loopback (including 0.0.0.0, which Linux routes to the
// local host), private and link-local networks (cloud metadata lives at
// 169.254.169.254), CGNAT, documentation/benchmark ranges, multicast and
// reserved space. It mirrors Xray's built-in "private" IP list so both
// kernels block the same set.
//
// The kernels enforce it twice: as a routing rule (fast rejection of IP
// destinations) and in the direct outbound itself, after DNS resolution and
// per UDP packet, so domains such as "localhost" and multi-destination UDP
// sessions cannot bypass it.
var BlockedDestinationCIDRs = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/3",
	"::/127",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
}

// BlockedDestinations returns a copy of BlockedDestinationCIDRs that callers
// may embed in generated configs.
func BlockedDestinations() []string {
	return append([]string(nil), BlockedDestinationCIDRs...)
}
