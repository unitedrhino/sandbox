// Package proxy provides network proxy functionality for sandboxed processes.
//
// This package implements a secure network access solution for sandboxed environments.
// It creates a veth pair to connect the sandbox network namespace
// with the host, and runs a SOCKS5 proxy with IP filtering.
//
// # Key Features
//
//   - IP Filtering: Built-in private IP blacklist (10.x, 172.16-31.x, 192.168.x, 127.x, 169.254.x, etc.)
//   - Whitelist Mode: Configure to allow only specific IP:Port combinations
//   - SOCKS5 Proxy: Standard protocol, works with curl, Python requests, etc.
//   - veth Pair Management: Automatic creation, configuration, and cleanup
//
// # Basic Usage (Blacklist Mode)
//
//	cfg := proxy.NetworkConfig{
//	    BridgeIP:  "10.233.1.2",
//	    SandboxIP: "10.233.1.1",
//	    ProxyPort: 1080,
//	}
//	netProxy := proxy.NewNetworkProxy(cfg)
//	netProxy.Setup(containerPID)
//	go netProxy.Start(ctx)
//	env := netProxy.GetProxyEnv() // ["ALL_PROXY=socks5://...", ...]
//
// # Whitelist Mode
//
//	rules := []proxy.AllowRule{
//	    {IP: net.ParseIP("8.8.8.8"), Port: 443},
//	}
//	filter, _ := proxy.NewWhitelistFilterWithPorts(rules)
//	netProxy.SetIPFilter(filter)
//
// # Requirements
//
// This package requires root privileges or CAP_NET_ADMIN capability to create
// network namespaces and veth pairs.
//
// # Integration with sandbox
//
// See README.md for detailed integration instructions with sandbox.
package proxy