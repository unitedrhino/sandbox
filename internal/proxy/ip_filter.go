// Package proxy provides network proxy functionality for sandboxed processes.
// It implements veth pair management and a SOCKS5 proxy server with IP filtering
// to allow external network access while blocking internal/private IPs.
package proxy

import (
	"fmt"
	"net"
)

// DefaultPrivateCIDRs contains the default list of private/internal IP CIDRs
// that should be blocked for security reasons.
var DefaultPrivateCIDRs = []string{
	"10.0.0.0/8",      // Private IPv4 (Class A)
	"172.16.0.0/12",   // Private IPv4 (Class B)
	"192.168.0.0/16",  // Private IPv4 (Class C)
	"127.0.0.0/8",     // Loopback
	"169.254.0.0/16",  // Link-local (cloud metadata services)
	"0.0.0.0/8",       // "This" network
	"224.0.0.0/4",     // Multicast
	"240.0.0.0/4",     // Reserved
	"255.255.255.255/32", // Broadcast
	"::1/128",         // IPv6 Loopback
	"fc00::/7",        // IPv6 Unique Local Addresses
	"fe80::/10",       // IPv6 Link-local
	"ff00::/8",        // IPv6 Multicast
	"::/128",          // IPv6 Unspecified
}

// AllowRule defines a single allow rule for IP:Port combinations.
type AllowRule struct {
	IP   net.IP
	Port int // 0 means any port
}

// IPFilterConfig holds configuration for the IP filter.
type IPFilterConfig struct {
	// Mode determines the filtering mode:
	// - "blacklist" (default): Block private IPs, allow all others
	// - "whitelist": Only allow explicitly allowed IPs/ports
	Mode string

	// BlockedCIDRs is the list of CIDRs to block (used in blacklist mode)
	BlockedCIDRs []string

	// AllowedRules is the list of IP:Port combinations to allow (used in whitelist mode)
	AllowedRules []AllowRule

	// AllowedCIDRs is the list of CIDRs to allow (used in whitelist mode)
	AllowedCIDRs []string

	// AllowedPorts is the list of ports to allow (0 means any port is allowed)
	// If empty, all ports are allowed in whitelist mode for matched IPs
	AllowedPorts []int
}

// IPFilter checks if an IP address belongs to private/internal networks.
type IPFilter struct {
	privateNets  []*net.IPNet
	allowedNets  []*net.IPNet
	mode         string
	allowedIPs   map[string][]int // IP -> allowed ports (empty means any port)
	allowedPorts []int
}

// NewIPFilter creates a new IPFilter with the given CIDR list.
// If cidrs is empty, DefaultPrivateCIDRs is used.
// This creates a blacklist filter (default mode).
func NewIPFilter(cidrs []string) (*IPFilter, error) {
	return NewIPFilterWithConfig(IPFilterConfig{
		Mode:         "blacklist",
		BlockedCIDRs: cidrs,
	})
}

// NewIPFilterWithConfig creates a new IPFilter with the given configuration.
func NewIPFilterWithConfig(cfg IPFilterConfig) (*IPFilter, error) {
	f := &IPFilter{
		mode:         cfg.Mode,
		allowedIPs:   make(map[string][]int),
		allowedPorts: cfg.AllowedPorts,
	}

	if cfg.Mode == "" {
		f.mode = "blacklist"
	}

	// Initialize blocked CIDRs for blacklist mode
	if f.mode == "blacklist" {
		blockedCIDRs := cfg.BlockedCIDRs
		if len(blockedCIDRs) == 0 {
			blockedCIDRs = DefaultPrivateCIDRs
		}

		f.privateNets = make([]*net.IPNet, 0, len(blockedCIDRs))
		for _, cidr := range blockedCIDRs {
			_, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
			}
			f.privateNets = append(f.privateNets, ipNet)
		}
	}

	// Initialize allowed rules for whitelist mode
	if f.mode == "whitelist" {
		f.allowedNets = make([]*net.IPNet, 0, len(cfg.AllowedCIDRs))
		for _, cidr := range cfg.AllowedCIDRs {
			_, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				return nil, fmt.Errorf("invalid allowed CIDR %q: %w", cidr, err)
			}
			f.allowedNets = append(f.allowedNets, ipNet)
		}
		for _, rule := range cfg.AllowedRules {
			ipKey := rule.IP.String()
			if rule.Port > 0 {
				f.allowedIPs[ipKey] = append(f.allowedIPs[ipKey], rule.Port)
			} else {
				// Port 0 means any port
				f.allowedIPs[ipKey] = []int{}
			}
		}
	}

	return f, nil
}

// NewWhitelistFilter creates a whitelist filter that only allows specified IPs/ports.
// If allowedPorts is empty, all ports are allowed for the specified IPs.
func NewWhitelistFilter(allowedIPs []string, allowedPorts []int) (*IPFilter, error) {
	cfg := IPFilterConfig{
		Mode:         "whitelist",
		AllowedPorts: allowedPorts,
	}

	for _, ipStr := range allowedIPs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP: %s", ipStr)
		}
		cfg.AllowedRules = append(cfg.AllowedRules, AllowRule{IP: ip, Port: 0})
	}

	return NewIPFilterWithConfig(cfg)
}

// NewWhitelistFilterWithPorts creates a whitelist filter with specific IP:Port rules.
func NewWhitelistFilterWithPorts(rules []AllowRule) (*IPFilter, error) {
	return NewIPFilterWithConfig(IPFilterConfig{
		Mode:         "whitelist",
		AllowedRules: rules,
	})
}

// IsPrivate returns true if the given IP address belongs to any private network.
// In whitelist mode, returns true for IPs NOT in the allowed list.
func (f *IPFilter) IsPrivate(ip net.IP) bool {
	if ip == nil {
		return true
	}

	// Normalize to 16-byte representation for comparison
	ip = ip.To16()
	if ip == nil {
		return true
	}

	if f.mode == "whitelist" {
		// In whitelist mode, "private" means "not in allowed list"
		if _, allowed := f.allowedIPs[ip.String()]; allowed {
			return false
		}
		for _, ipNet := range f.allowedNets {
			if ipNet.Contains(ip) {
				return false
			}
		}
		return true
	}

	// Blacklist mode
	for _, ipNet := range f.privateNets {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

// IsPublic returns true if the given IP address is a public (non-private) address.
func (f *IPFilter) IsPublic(ip net.IP) bool {
	return !f.IsPrivate(ip)
}

// IsPortAllowed checks if the given port is allowed.
// Returns true if the port is allowed or if no port restrictions are set.
func (f *IPFilter) IsPortAllowed(ip net.IP, port int) bool {
	if f.mode == "blacklist" {
		// In blacklist mode, just check if IP is blocked
		return f.IsPublic(ip)
	}

	// Whitelist mode
	ipStr := ip.String()
	allowedPorts, ipAllowed := f.allowedIPs[ipStr]
	if ipAllowed {
		// If allowedPorts for this IP is empty, any port is allowed
		if len(allowedPorts) == 0 {
			// Check global port restrictions
			if len(f.allowedPorts) == 0 {
				return true
			}
			return f.containsPort(f.allowedPorts, port)
		}

		// Check if the specific port is allowed for this IP
		return f.containsPort(allowedPorts, port)
	}

	for _, ipNet := range f.allowedNets {
		if ipNet.Contains(ip) {
			if len(f.allowedPorts) == 0 {
				return true
			}
			return f.containsPort(f.allowedPorts, port)
		}
	}
	return false
}

func (f *IPFilter) containsPort(ports []int, port int) bool {
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

// CheckHost resolves the host and checks if any of its IPs are private.
// Returns an error if the host resolves to a private IP address.
func (f *IPFilter) CheckHost(host string) error {
	// First, try to parse as IP directly
	if ip := net.ParseIP(host); ip != nil {
		if f.IsPrivate(ip) {
			return fmt.Errorf("host %s is a private/blocked IP address", host)
		}
		return nil
	}

	// Resolve host to IPs
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("failed to resolve host %s: %w", host, err)
	}

	for _, ip := range ips {
		if f.IsPrivate(ip) {
			return fmt.Errorf("host %s resolves to private/blocked IP %s", host, ip)
		}
	}
	return nil
}

// CheckHostWithPort resolves the host and checks if the IP:Port combination is allowed.
func (f *IPFilter) CheckHostWithPort(host string, port int) error {
	// First, try to parse as IP directly
	if ip := net.ParseIP(host); ip != nil {
		if !f.IsPortAllowed(ip, port) {
			return fmt.Errorf("IP %s:%d is not allowed", host, port)
		}
		return nil
	}

	// Resolve host to IPs
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("failed to resolve host %s: %w", host, err)
	}

	for _, ip := range ips {
		if !f.IsPortAllowed(ip, port) {
			return fmt.Errorf("host %s resolves to IP %s:%d which is not allowed", host, ip, port)
		}
	}
	return nil
}

// PrivateCIDRs returns the list of private CIDRs configured in the filter.
func (f *IPFilter) PrivateCIDRs() []string {
	cidrs := make([]string, len(f.privateNets))
	for i, ipNet := range f.privateNets {
		cidrs[i] = ipNet.String()
	}
	return cidrs
}

// Mode returns the current filtering mode.
func (f *IPFilter) Mode() string {
	return f.mode
}

// AllowedIPs returns the list of allowed IPs (for whitelist mode).
func (f *IPFilter) AllowedIPs() []string {
	ips := make([]string, 0, len(f.allowedIPs))
	for ip := range f.allowedIPs {
		ips = append(ips, ip)
	}
	return ips
}
