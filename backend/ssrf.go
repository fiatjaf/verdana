package backend

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"

	"verdana/backend/wasmhost"
)

// isAllowedRelayURL verifies that a relay/NIP-11 URL uses ws/wss/http/https,
// does not target localhost, and resolves exclusively to non-forbidden IPs
// (blocking loopback, link-local / cloud metadata, multicast, unspecified,
// and NAT64/IPv4-compat embeddings of forbidden IPv4 targets).
func isAllowedRelayURL(ctx context.Context, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("invalid relay url")
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "ws", "wss", "http", "https":
	default:
		return errors.New("unsupported relay scheme: " + u.Scheme)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errors.New("local relay addresses are not allowed")
	}
	if ip := net.ParseIP(host); ip != nil {
		if wasmhost.IsForbiddenIP(ip) {
			return errors.New("relay resolves to a forbidden local address")
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return errors.New("relay host resolved to no addresses")
	}
	for _, ip := range ips {
		if wasmhost.IsForbiddenIP(ip.IP) {
			return errors.New("relay resolves to a forbidden local address")
		}
	}
	return nil
}
