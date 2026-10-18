package fetch

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// A web page decides which other addresses link2epub connects to: it can
// redirect, and it names its images and cover. Without a guard, a page on
// the internet could make link2epub send requests to the reader's own
// computer or to devices on their network, such as a router's admin page.
// So when the page itself is on the internet, connections to local and
// private addresses are refused. The check is made on the address actually
// dialled, after DNS lookup, so a hostname that resolves to a local address
// is caught too.

// BlockedError reports a refused connection to a local or private address.
type BlockedError struct {
	Addr netip.Addr
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("connection to %s refused: it is a local or private address", e.Addr)
}

// IsLocal reports whether host is, or resolves to, an address that isn't on
// the public internet, such as localhost or a server on the home network.
// Local connections are allowed only when the page itself is local.
func IsLocal(ctx context.Context, host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return !isPublic(ip)
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return false
	}
	for _, ip := range addrs {
		if !isPublic(ip) {
			return true
		}
	}
	return false
}

// nonPublic lists ranges that aren't on the public internet and that
// netip.Addr's methods don't cover.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT, also used by Tailscale
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // network benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved
}

// isPublic reports whether ip is a public internet address. Loopback,
// private, link-local (which includes cloud metadata services), multicast
// and reserved addresses are not.
func isPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// permitted decides whether a guarded client may connect to an address.
// Tests replace it, because their servers are all on localhost.
var permitted = func(ap netip.AddrPort) bool { return isPublic(ap.Addr()) }

// newClient returns the HTTP client for pages and images. Unless
// allowLocal is set, it refuses to connect to local and private addresses.
func newClient(allowLocal bool) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if !allowLocal {
		t.DialContext = guardedDial(proxyAddrs())
	}
	return &http.Client{Timeout: 60 * time.Second, Transport: t}
}

// guardedDial returns a dial function that refuses local and private
// addresses, except for the proxies in proxies: a proxy is the user's own
// choice and is often on their network. Requests sent through a proxy are
// dialled by the proxy, so the guard can't check them.
func guardedDial(proxies map[string]bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	// The same settings as http.DefaultTransport.
	plain := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	guarded := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return err
			}
			if !permitted(ap) {
				return &BlockedError{Addr: ap.Addr().Unmap()}
			}
			return nil
		}}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if proxies[addr] {
			return plain.DialContext(ctx, network, addr)
		}
		return guarded.DialContext(ctx, network, addr)
	}
}

// proxyAddrs returns the host:port of the proxies set in the environment
// (HTTP_PROXY and HTTPS_PROXY), which http.DefaultTransport uses.
func proxyAddrs() map[string]bool {
	cfg := httpproxy.FromEnvironment()
	addrs := map[string]bool{}
	for _, raw := range []string{cfg.HTTPProxy, cfg.HTTPSProxy} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			// A proxy may be given without a scheme, as "proxy:3128".
			if u, err = url.Parse("http://" + raw); err != nil {
				continue
			}
		}
		port := u.Port()
		if port == "" {
			switch u.Scheme {
			case "https":
				port = "443"
			case "socks5", "socks5h":
				port = "1080"
			default:
				port = "80"
			}
		}
		addrs[net.JoinHostPort(u.Hostname(), port)] = true
	}
	return addrs
}
