package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// SOCKS5 protocol constants
const (
	socks5Version = 0x05

	// Authentication methods
	authNone     = 0x00
	authNoAccept = 0xFF

	// Commands
	cmdConnect = 0x01
	cmdBind    = 0x02
	cmdUDP     = 0x03

	// Address types
	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	// Reply codes
	repSuccess          = 0x00
	repGeneralFailure   = 0x01
	repConnectionRefused = 0x05
	repAddressNotSupported = 0x08
)

// Socks5Config holds configuration for the SOCKS5 server.
type Socks5Config struct {
	// Addr is the address to listen on (e.g., "10.233.1.2:1080")
	Addr string

	// IPFilter is used to block private/internal IPs.
	// If nil, DefaultPrivateCIDRs will be used.
	IPFilter *IPFilter

	// DialTimeout is the timeout for connecting to target hosts.
	// Default is 10 seconds if not set.
	DialTimeout time.Duration

	// ReadTimeout is the timeout for reading from client.
	// Default is 30 seconds if not set.
	ReadTimeout time.Duration
}

// Socks5Server implements a SOCKS5 proxy server with IP filtering.
type Socks5Server struct {
	config    Socks5Config
	ipFilter  *IPFilter
	listener  net.Listener
	running   bool
	mu        sync.Mutex
	connWg    sync.WaitGroup
}

// NewSocks5Server creates a new SOCKS5 server.
func NewSocks5Server(cfg Socks5Config) (*Socks5Server, error) {
	if cfg.Addr == "" {
		cfg.Addr = "0.0.0.0:1080"
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 30 * time.Second
	}

	ipFilter := cfg.IPFilter
	if ipFilter == nil {
		var err error
		ipFilter, err = NewIPFilter(nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create IP filter: %w", err)
		}
	}

	return &Socks5Server{
		config:   cfg,
		ipFilter: ipFilter,
	}, nil
}

// Start starts the SOCKS5 server. It blocks until the context is cancelled.
func (s *Socks5Server) Start(ctx context.Context) error {
	var err error
	s.listener, err = net.Listen("tcp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.Addr, err)
	}

	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	// Start goroutine to close listener when context is done
	go func() {
		<-ctx.Done()
		s.Stop()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			running := s.running
			s.mu.Unlock()
			if !running {
				return nil // Server stopped
			}
			return fmt.Errorf("accept error: %w", err)
		}

		s.connWg.Add(1)
		go func() {
			defer s.connWg.Done()
			s.handleConnection(ctx, conn)
		}()
	}
}

// Stop stops the SOCKS5 server.
func (s *Socks5Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.running = false
	if s.listener != nil {
		s.listener.Close()
	}
	s.connWg.Wait()
	return nil
}

// Addr returns the address the server is listening on.
func (s *Socks5Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Socks5Server) handleConnection(ctx context.Context, clientConn net.Conn) {
	defer clientConn.Close()

	// Set read deadline
	if s.config.ReadTimeout > 0 {
		clientConn.SetDeadline(time.Now().Add(s.config.ReadTimeout))
	}

	// Phase 1: Handshake
	if err := s.handleHandshake(clientConn); err != nil {
		// Handshake failed, just close connection
		return
	}

	// Phase 2: Request
	if err := s.handleRequest(ctx, clientConn); err != nil {
		// Request handling failed
		return
	}
}

func (s *Socks5Server) handleHandshake(conn net.Conn) error {
	// Read client greeting
	// +----+----------+----------+
	// |VER | NMETHODS | METHODS  |
	// +----+----------+----------+
	// | 1  |    1     | 1 to 255 |
	// +----+----------+----------+

	buf := make([]byte, 257)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read greeting: %w", err)
	}
	if n < 2 {
		return fmt.Errorf("greeting too short")
	}

	if buf[0] != socks5Version {
		return fmt.Errorf("unsupported SOCKS version: %d", buf[0])
	}

	nMethods := int(buf[1])
	if n < 2+nMethods {
		return fmt.Errorf("greeting incomplete")
	}

	// Check if no-auth method is supported
	hasNoAuth := false
	for i := 0; i < nMethods; i++ {
		if buf[2+i] == authNone {
			hasNoAuth = true
			break
		}
	}

	// Send server choice
	// +----+--------+
	// |VER | METHOD |
	// +----+--------+
	// | 1  |   1    |
	// +----+--------+
	response := []byte{socks5Version, authNoAccept}
	if hasNoAuth {
		response[1] = authNone
	}

	if _, err := conn.Write(response); err != nil {
		return fmt.Errorf("write method choice: %w", err)
	}

	if !hasNoAuth {
		return fmt.Errorf("client requires authentication, not supported")
	}

	return nil
}

func (s *Socks5Server) handleRequest(ctx context.Context, conn net.Conn) error {
	// Read request
	// +----+-----+-------+------+----------+----------+
	// |VER | CMD |  RSV  | ATYP | DST.ADDR | DST.PORT |
	// +----+-----+-------+------+----------+----------+
	// | 1  |  1  | X'00' |  1   | Variable |    2     |
	// +----+-----+-------+------+----------+----------+

	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return fmt.Errorf("read request header: %w", err)
	}

	if buf[0] != socks5Version {
		return fmt.Errorf("unsupported SOCKS version: %d", buf[0])
	}

	cmd := buf[1]
	// buf[2] is reserved
	atyp := buf[3]

	// Read destination address
	var targetHost string
	var targetIP net.IP

	switch atyp {
	case atypIPv4:
		ipBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return fmt.Errorf("read IPv4 address: %w", err)
		}
		targetIP = net.IP(ipBuf)
		targetHost = targetIP.String()

	case atypDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return fmt.Errorf("read domain length: %w", err)
		}
		domainLen := int(lenBuf[0])
		domainBuf := make([]byte, domainLen)
		if _, err := io.ReadFull(conn, domainBuf); err != nil {
			return fmt.Errorf("read domain: %w", err)
		}
		targetHost = string(domainBuf)

	case atypIPv6:
		ipBuf := make([]byte, 16)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return fmt.Errorf("read IPv6 address: %w", err)
		}
		targetIP = net.IP(ipBuf)
		targetHost = targetIP.String()

	default:
		s.sendReply(conn, repAddressNotSupported, "0.0.0.0", 0)
		return fmt.Errorf("unsupported address type: %d", atyp)
	}

	// Read destination port
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return fmt.Errorf("read port: %w", err)
	}
	targetPort := binary.BigEndian.Uint16(portBuf)

	// Only support CONNECT command
	if cmd != cmdConnect {
		s.sendReply(conn, repGeneralFailure, "0.0.0.0", 0)
		return fmt.Errorf("unsupported command: %d", cmd)
	}

	// IP filtering
	if targetIP != nil {
		// Direct IP address
		if !s.ipFilter.IsPortAllowed(targetIP, int(targetPort)) {
			s.sendReply(conn, repConnectionRefused, "0.0.0.0", 0)
			return fmt.Errorf("target IP %s:%d is blocked", targetIP, targetPort)
		}
	} else {
		// Domain name - need to resolve and check
		if err := s.ipFilter.CheckHostWithPort(targetHost, int(targetPort)); err != nil {
			s.sendReply(conn, repConnectionRefused, "0.0.0.0", 0)
			return fmt.Errorf("target host %s is blocked: %w", targetHost, err)
		}
	}

	// Connect to target
	targetAddr := fmt.Sprintf("%s:%d", targetHost, targetPort)
	dialer := &net.Dialer{Timeout: s.config.DialTimeout}
	targetConn, err := dialer.DialContext(ctx, "tcp", targetAddr)
	if err != nil {
		s.sendReply(conn, repConnectionRefused, "0.0.0.0", 0)
		return fmt.Errorf("connect to %s failed: %w", targetAddr, err)
	}
	defer targetConn.Close()

	// Send success reply
	// +----+-----+-------+------+----------+----------+
	// |VER | REP |  RSV  | ATYP | BND.ADDR | BND.PORT |
	// +----+-----+-------+------+----------+----------+
	localAddr := targetConn.LocalAddr().(*net.TCPAddr)
	s.sendReply(conn, repSuccess, localAddr.IP.String(), uint16(localAddr.Port))

	// Clear deadline for relay
	conn.SetDeadline(time.Time{})

	// Relay data bidirectionally
	go func() {
		io.Copy(targetConn, conn)
		targetConn.Close()
	}()
	io.Copy(conn, targetConn)

	return nil
}

func (s *Socks5Server) sendReply(conn net.Conn, rep byte, bindIP string, bindPort uint16) error {
	var atyp byte
	var addr []byte

	ip := net.ParseIP(bindIP)
	if ip == nil {
		// Domain (shouldn't happen in bind address, but handle it)
		atyp = atypDomain
		addr = []byte(bindIP)
	} else if ip4 := ip.To4(); ip4 != nil {
		atyp = atypIPv4
		addr = ip4
	} else {
		atyp = atypIPv6
		addr = ip
	}

	// Build reply
	reply := make([]byte, 0, 6+len(addr))
	reply = append(reply, socks5Version)
	reply = append(reply, rep)
	reply = append(reply, 0x00) // reserved
	reply = append(reply, atyp)
	reply = append(reply, addr...)
	reply = append(reply, 0, 0) // port placeholder

	// Set port
	binary.BigEndian.PutUint16(reply[len(reply)-2:], bindPort)

	_, err := conn.Write(reply)
	return err
}
