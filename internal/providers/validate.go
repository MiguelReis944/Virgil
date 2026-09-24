package providers

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
)

func validateLocalBaseURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid local base_url")
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("local base_url must use a loopback address")
	}
	return nil
}

// validateBaseURL rejects loopback and link-local base_url values to prevent SSRF.
// Private RFC-1918 addresses generate a warning but are allowed (self-hosted providers).
func validateBaseURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid base_url: %w", err)
	}
	host := u.Hostname()
	if host == "localhost" {
		return fmt.Errorf("base_url must not use loopback address %q", host)
	}
	ips, err := net.LookupHost(host)
	if err != nil {
		// If lookup fails (e.g., offline), skip — don't block valid providers.
		return nil
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() {
			return fmt.Errorf("base_url %q resolves to loopback address", rawURL)
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("base_url %q resolves to link-local address", rawURL)
		}
		if isPrivate(ip) {
			slog.Warn("provider base_url resolves to private address; ensure this is intentional", "url", rawURL)
		}
	}
	return nil
}

// isPrivate returns true for RFC-1918 private addresses.
func isPrivate(ip net.IP) bool {
	private := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}
	for _, cidr := range private {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return true
		}
	}
	return false
}
