//go:build linux
// +build linux

package proxy

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// NetworkConfig holds configuration for the network proxy.
type NetworkConfig struct {
	// BridgeIP is the IP address for the host-side veth (e.g., "10.233.1.2")
	BridgeIP string

	// SandboxIP is the IP address for the sandbox-side veth (e.g., "10.233.1.1")
	SandboxIP string

	// ProxyPort is the port for the SOCKS5 proxy (default: 1080)
	ProxyPort int

	// CIDR is the network CIDR (e.g., "10.233.1.0/24")
	CIDR string

	// VethHost is the name for the host-side veth device (default: "veth_host")
	VethHost string

	// VethSandbox is the name for the sandbox-side veth device (default: "veth_sandbox")
	VethSandbox string
}

// DefaultNetworkConfig returns a default network configuration.
func DefaultNetworkConfig() NetworkConfig {
	return NetworkConfig{
		BridgeIP:    "10.233.1.2",
		SandboxIP:   "10.233.1.1",
		ProxyPort:   1080,
		CIDR:        "10.233.1.0/24",
		VethHost:    "veth_host",
		VethSandbox: "veth_sandbox",
	}
}

// NetworkProxy manages the network setup for a sandboxed process.
// It creates a veth pair to connect the sandbox network namespace
// with the host, and runs a SOCKS5 proxy for controlled network access.
type NetworkProxy struct {
	config       NetworkConfig
	vethHost     netlink.Link
	vethSandbox  netlink.Link
	socks5       *Socks5Server
	ipFilter     *IPFilter
	containerPID int
	mu           sync.Mutex
	running      bool
	cleanupFns   []func() error
}

// NewNetworkProxy creates a new NetworkProxy with the given configuration.
func NewNetworkProxy(cfg NetworkConfig) *NetworkProxy {
	// Set defaults
	if cfg.ProxyPort == 0 {
		cfg.ProxyPort = 1080
	}
	if cfg.CIDR == "" {
		cfg.CIDR = "10.233.1.0/24"
	}
	if cfg.BridgeIP == "" {
		cfg.BridgeIP = "10.233.1.2"
	}
	if cfg.SandboxIP == "" {
		cfg.SandboxIP = "10.233.1.1"
	}
	if cfg.VethHost == "" {
		cfg.VethHost = "veth_host"
	}
	if cfg.VethSandbox == "" {
		cfg.VethSandbox = "veth_sandbox"
	}

	return &NetworkProxy{
		config: cfg,
	}
}

// Setup creates the veth pair and configures networking for the sandbox.
// This must be called after the container process has started (has a PID).
func (p *NetworkProxy) Setup(containerPID int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return fmt.Errorf("network proxy already running")
	}

	p.containerPID = containerPID

	// Create default IP filter only when the caller has not provided one.
	if p.ipFilter == nil {
		var err error
		p.ipFilter, err = NewIPFilter(nil)
		if err != nil {
			return fmt.Errorf("create IP filter: %w", err)
		}
	}

	// Step 1: Create veth pair
	if err := p.createVethPair(); err != nil {
		return fmt.Errorf("create veth pair: %w", err)
	}

	// Step 2: Configure host-side veth
	if err := p.configureHostVeth(); err != nil {
		p.cleanupVeth()
		return fmt.Errorf("configure host veth: %w", err)
	}

	// Step 3: Move sandbox-side veth to container network namespace
	if err := p.moveVethToNetns(); err != nil {
		p.cleanupVeth()
		return fmt.Errorf("move veth to netns: %w", err)
	}

	// Step 4: Configure sandbox-side veth inside the network namespace
	if err := p.configureSandboxVeth(); err != nil {
		p.cleanupVeth()
		return fmt.Errorf("configure sandbox veth: %w", err)
	}

	// Step 5: Create SOCKS5 server
	proxyAddr := fmt.Sprintf("%s:%d", p.config.BridgeIP, p.config.ProxyPort)
	socks5Server, err := NewSocks5Server(Socks5Config{
		Addr:      proxyAddr,
		IPFilter:  p.ipFilter,
		DialTimeout: 10 * time.Second,
	})
	if err != nil {
		p.cleanupVeth()
		return fmt.Errorf("create socks5 server: %w", err)
	}
	p.socks5 = socks5Server

	p.running = true
	return nil
}

// Start starts the SOCKS5 proxy server.
// This should be called after Setup and before executing commands in the sandbox.
func (p *NetworkProxy) Start(ctx context.Context) error {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return fmt.Errorf("network proxy not set up")
	}
	p.mu.Unlock()

	return p.socks5.Start(ctx)
}

// Stop stops the SOCKS5 proxy server.
func (p *NetworkProxy) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.socks5 != nil {
		if err := p.socks5.Stop(); err != nil {
			return err
		}
	}
	p.running = false
	return nil
}

// Cleanup removes all network resources (veth pair, etc).
func (p *NetworkProxy) Cleanup() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Stop socks5 server
	if p.socks5 != nil {
		p.socks5.Stop()
	}

	// Cleanup veth
	p.cleanupVeth()

	p.running = false
	return nil
}

// GetProxyEnv returns environment variables for using the proxy.
// These should be set when executing commands in the sandbox.
func (p *NetworkProxy) GetProxyEnv() []string {
	proxyURL := fmt.Sprintf("socks5://%s:%d", p.config.BridgeIP, p.config.ProxyPort)
	return []string{
		fmt.Sprintf("ALL_PROXY=%s", proxyURL),
		fmt.Sprintf("all_proxy=%s", proxyURL),
		fmt.Sprintf("HTTP_PROXY=%s", proxyURL),
		fmt.Sprintf("HTTPS_PROXY=%s", proxyURL),
		fmt.Sprintf("http_proxy=%s", proxyURL),
		fmt.Sprintf("https_proxy=%s", proxyURL),
	}
}

// GetProxyURL returns the proxy URL.
func (p *NetworkProxy) GetProxyURL() string {
	return fmt.Sprintf("socks5://%s:%d", p.config.BridgeIP, p.config.ProxyPort)
}

// GetBridgeIP returns the bridge IP address.
func (p *NetworkProxy) GetBridgeIP() string {
	return p.config.BridgeIP
}

// GetSandboxIP returns the sandbox IP address.
func (p *NetworkProxy) GetSandboxIP() string {
	return p.config.SandboxIP
}

func (p *NetworkProxy) createVethPair() error {
	// Create unique names based on PID to avoid conflicts
	hostName := fmt.Sprintf("%s_%d", p.config.VethHost, p.containerPID)
	sandboxName := fmt.Sprintf("%s_%d", p.config.VethSandbox, p.containerPID)

	// Truncate if too long (Linux interface names are limited to 15 chars)
	if len(hostName) > 15 {
		hostName = hostName[:15]
	}
	if len(sandboxName) > 15 {
		sandboxName = sandboxName[:15]
	}

	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: hostName,
		},
		PeerName: sandboxName,
	}

	if err := netlink.LinkAdd(veth); err != nil {
		return fmt.Errorf("add veth link: %w", err)
	}

	// Store links
	var err error
	p.vethHost, err = netlink.LinkByName(hostName)
	if err != nil {
		return fmt.Errorf("get host veth: %w", err)
	}

	p.vethSandbox, err = netlink.LinkByName(sandboxName)
	if err != nil {
		return fmt.Errorf("get sandbox veth: %w", err)
	}

	// Capture the host-side link name at creation time so cleanup does not depend
	// on p.vethHost still being non-nil later. Cleanup() first clears p.vethHost,
	// then runs cleanupFns; referencing p.vethHost here can cause a nil-pointer panic.
	p.cleanupFns = append(p.cleanupFns, func() error {
		link, err := netlink.LinkByName(hostName)
		if err != nil {
			return nil
		}
		return netlink.LinkDel(link)
	})

	return nil
}

func (p *NetworkProxy) configureHostVeth() error {
	// Parse bridge IP
	addr, err := netlink.ParseAddr(fmt.Sprintf("%s/24", p.config.BridgeIP))
	if err != nil {
		return fmt.Errorf("parse bridge IP: %w", err)
	}

	// Add IP address
	if err := netlink.AddrAdd(p.vethHost, addr); err != nil {
		return fmt.Errorf("add address to host veth: %w", err)
	}

	// Bring up the interface
	if err := netlink.LinkSetUp(p.vethHost); err != nil {
		return fmt.Errorf("set host veth up: %w", err)
	}

	return nil
}

func (p *NetworkProxy) moveVethToNetns() error {
	// Get the network namespace handle for the container
	ns, err := netns.GetFromPid(p.containerPID)
	if err != nil {
		return fmt.Errorf("get netns from pid %d: %w", p.containerPID, err)
	}
	defer ns.Close()

	// Move the sandbox-side veth to the container's network namespace
	if err := netlink.LinkSetNsFd(p.vethSandbox, int(ns)); err != nil {
		return fmt.Errorf("move veth to netns: %w", err)
	}

	return nil
}

func (p *NetworkProxy) configureSandboxVeth() error {
	// We need to execute commands in the container's network namespace
	// Use nsenter or /proc/{pid}/ns/net with ip netns exec equivalent

	// Get the network namespace handle
	ns, err := netns.GetFromPid(p.containerPID)
	if err != nil {
		return fmt.Errorf("get netns from pid %d: %w", p.containerPID, err)
	}
	defer ns.Close()

	// Execute configuration in the network namespace
	// We use nsenter to run commands in the target namespace
	commands := [][]string{
		// Bring up loopback
		{"ip", "link", "set", "lo", "up"},
		// Keep Docker-provided eth0 intact. The sandbox proxy interface uses a dedicated name.
		{"ip", "link", "set", p.vethSandbox.Attrs().Name, "name", "claw0"},
		// Add IP address
		{"ip", "addr", "add", fmt.Sprintf("%s/24", p.config.SandboxIP), "dev", "claw0"},
		// Bring up claw0. The connected route to BridgeIP is created automatically.
		{"ip", "link", "set", "claw0", "up"},
	}

	for _, args := range commands {
		if err := p.runInSandboxNetns(args...); err != nil {
			return fmt.Errorf("run command %v: %w", args, err)
		}
	}

	if err := p.applySandboxEgressPolicy(); err != nil {
		return fmt.Errorf("apply sandbox egress policy: %w", err)
	}

	return nil
}

func (p *NetworkProxy) runInSandboxNetns(args ...string) error {
	cmd := exec.Command("nsenter", "-t", fmt.Sprintf("%d", p.containerPID), "-n")
	cmd.Args = append(cmd.Args, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (p *NetworkProxy) applySandboxEgressPolicy() error {
	policyCommands := [][]string{
		{"iptables", "-w", "-F", "OUTPUT"},
		{"iptables", "-w", "-P", "OUTPUT", "DROP"},
		{"iptables", "-w", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},
		{"iptables", "-w", "-A", "OUTPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
		{"iptables", "-w", "-A", "OUTPUT", "-d", p.config.BridgeIP, "-p", "tcp", "--dport", fmt.Sprintf("%d", p.config.ProxyPort), "-j", "ACCEPT"},
		{"iptables", "-w", "-A", "OUTPUT", "-p", "udp", "-j", "DROP"},
	}

	for _, args := range policyCommands {
		if err := p.runInSandboxNetns(args...); err != nil {
			return fmt.Errorf("%v: %w", args, err)
		}
	}

	return nil
}

func (p *NetworkProxy) cleanupVeth() {
	// Delete veth pair (deleting one end deletes both)
	if p.vethHost != nil {
		netlink.LinkDel(p.vethHost)
		p.vethHost = nil
		p.vethSandbox = nil
	}

	// Run additional cleanup functions
	for _, fn := range p.cleanupFns {
		if fn == nil {
			continue
		}
		_ = fn()
	}
	p.cleanupFns = nil
}

// SetIPFilter allows setting a custom IP filter.
// Must be called before Setup.
func (p *NetworkProxy) SetIPFilter(filter *IPFilter) {
	p.ipFilter = filter
}

// AddBlockedCIDRs adds additional CIDRs to the block list.
// Must be called before Setup.
func (p *NetworkProxy) AddBlockedCIDRs(cidrs []string) error {
	// Create a new filter with combined CIDRs
	allCIDRs := append(DefaultPrivateCIDRs, cidrs...)
	filter, err := NewIPFilter(allCIDRs)
	if err != nil {
		return err
	}
	p.ipFilter = filter
	return nil
}

// GenerateUniqueIPs generates unique IP addresses for a sandbox instance.
// This is useful when running multiple sandboxes concurrently.
func GenerateUniqueIPs(instanceID int) (bridgeIP, sandboxIP string) {
	// Use 10.233.0.0/16, assigning adjacent pairs: (2,3), (4,5), ... (252,253)
	// Avoids network (0), broadcast (255), and invalid .256 addresses.
	// Maximum pairs per octet3: 126  => total 256 * 126 = 32256 unique pairs.
	const pairsPerOctet = 126
	totalPairs := 256 * pairsPerOctet

	instanceID = ((instanceID - 1) % totalPairs) + 1

	octet3 := (instanceID - 1) / pairsPerOctet
	pairIdx := (instanceID - 1) % pairsPerOctet

	baseOctet := pairIdx*2 + 2 // starts at 2, ends at 253
	sandboxIP = fmt.Sprintf("10.233.%d.%d", octet3, baseOctet)
	bridgeIP = fmt.Sprintf("10.233.%d.%d", octet3, baseOctet+1)

	return bridgeIP, sandboxIP
}

// ParseIPFromCIDR extracts the IP from a CIDR string.
func ParseIPFromCIDR(cidr string) (net.IP, error) {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("parse CIDR %q: %w", cidr, err)
	}
	return ip, nil
}

// IsRoot checks if the current process is running as root.
func IsRoot() bool {
	return os.Geteuid() == 0
}

// HasNetAdminCapability checks if the process has CAP_NET_ADMIN capability.
// This is required for creating network namespaces and veth pairs.
func HasNetAdminCapability() bool {
	// Simple check: try to create a network namespace
	// If we can, we likely have the capability
	ns, err := netns.New()
	if err != nil {
		return false
	}
	ns.Close()
	return true
}

// SetNames sets custom veth names.
func (p *NetworkProxy) SetNames(hostName, sandboxName string) {
	if hostName != "" {
		p.config.VethHost = hostName
	}
	if sandboxName != "" {
		p.config.VethSandbox = sandboxName
	}
}

// Validate checks if the configuration is valid.
func (p *NetworkProxy) Validate() error {
	// Check IPs
	bridgeIP := net.ParseIP(p.config.BridgeIP)
	if bridgeIP == nil {
		return fmt.Errorf("invalid bridge IP: %s", p.config.BridgeIP)
	}

	sandboxIP := net.ParseIP(p.config.SandboxIP)
	if sandboxIP == nil {
		return fmt.Errorf("invalid sandbox IP: %s", p.config.SandboxIP)
	}

	// Check CIDR
	_, _, err := net.ParseCIDR(p.config.CIDR)
	if err != nil {
		return fmt.Errorf("invalid CIDR: %s", p.config.CIDR)
	}

	// Check port
	if p.config.ProxyPort < 1 || p.config.ProxyPort > 65535 {
		return fmt.Errorf("invalid proxy port: %d", p.config.ProxyPort)
	}

	// Check veth names
	if strings.Contains(p.config.VethHost, " ") {
		return fmt.Errorf("invalid veth host name: %s", p.config.VethHost)
	}
	if strings.Contains(p.config.VethSandbox, " ") {
		return fmt.Errorf("invalid veth sandbox name: %s", p.config.VethSandbox)
	}

	return nil
}
