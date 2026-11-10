package platform

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type addressKey struct{}
type dnsEntry struct {
	addresses []netip.Addr
	until     time.Time
}

var proxyDNS = struct {
	sync.Mutex
	hosts map[string]dnsEntry
}{hosts: map[string]dnsEntry{}}

func validateProxyConfig(c Config) error {
	if len(c.TrustedProxyHosts)+len(c.TrustedProxyCIDRs) > 16 {
		return errors.New("at most 16 trusted proxies allowed")
	}
	for _, host := range c.TrustedProxyHosts {
		if len(host) < 1 || len(host) > 253 || strings.ContainsAny(host, " /\\:@\n\r\t") {
			return errors.New("invalid TRUSTED_PROXY_HOSTS")
		}
	}
	for _, cidr := range c.TrustedProxyCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return errors.New("invalid TRUSTED_PROXY_CIDRS")
		}
	}
	return nil
}
func remoteAddress(value string) netip.Addr {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		host = value
	}
	ip, _ := netip.ParseAddr(host)
	return ip.Unmap()
}
func trustedProxy(ctx context.Context, ip netip.Addr, c Config) bool {
	if !ip.IsValid() {
		return false
	}
	for _, value := range c.TrustedProxyCIDRs {
		prefix, err := netip.ParsePrefix(value)
		if err == nil && prefix.Contains(ip) {
			return true
		}
	}
	for _, host := range c.TrustedProxyHosts {
		proxyDNS.Lock()
		entry, ok := proxyDNS.hosts[host]
		proxyDNS.Unlock()
		if !ok || time.Now().After(entry.until) {
			lookup, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			addresses, _ := net.DefaultResolver.LookupNetIP(lookup, "ip", host)
			cancel()
			entry = dnsEntry{addresses: addresses, until: time.Now().Add(10 * time.Second)}
			proxyDNS.Lock()
			if len(proxyDNS.hosts) >= 16 {
				clear(proxyDNS.hosts)
			}
			proxyDNS.hosts[host] = entry
			proxyDNS.Unlock()
		}
		for _, address := range entry.addresses {
			if address.Unmap() == ip {
				return true
			}
		}
	}
	return false
}

// ResolveClientAddress walks from the socket peer toward the client, stopping at
// the first untrusted hop. Untrusted prefixes cannot override that address.
func ResolveClientAddress(r *http.Request, c Config) string {
	peer := remoteAddress(r.RemoteAddr)
	if !peer.IsValid() {
		return "unknown"
	}
	values := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(values) > 16 {
		return peer.String()
	}
	for i := len(values) - 1; i >= 0; i-- {
		if !trustedProxy(r.Context(), peer, c) {
			break
		}
		next, err := netip.ParseAddr(strings.TrimSpace(values[i]))
		if err != nil {
			break
		}
		peer = next.Unmap()
	}
	return peer.String()
}
func ClientAddressMiddleware(c Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), addressKey{}, ResolveClientAddress(r, c))))
		})
	}
}
func ClientAddress(r *http.Request) string {
	if address, ok := r.Context().Value(addressKey{}).(string); ok {
		return address
	}
	address := remoteAddress(r.RemoteAddr)
	if address.IsValid() {
		return address.String()
	}
	return "unknown"
}
func ClientKey(r *http.Request, secret string) string {
	return KeyedHash(secret, "client-address:"+ClientAddress(r))
}
