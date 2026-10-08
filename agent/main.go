package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	addr := strings.TrimSpace(os.Getenv("TUNNEL_ADDR"))
	token := os.Getenv("TUNNEL_TOKEN")
	if addr == "" || token == "" {
		log.Fatal("TUNNEL_ADDR and TUNNEL_TOKEN are required")
	}
	if strings.Contains(addr, "://") {
		log.Fatal("TUNNEL_ADDR must be host:port, not a URL")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		log.Fatalf("TUNNEL_ADDR must be host:port: %v", err)
	}
	if len(token) < 32 || len(token) > 512 {
		log.Fatal("TUNNEL_TOKEN must be 32 to 512 characters")
	}
	ports, err := parsePorts(env("ALLOW_PORTS", "443,8443,1080,8081"))
	if err != nil {
		log.Fatalf("ALLOW_PORTS: %v", err)
	}
	allow := make(map[uint16]struct{}, len(ports))
	for _, p := range ports {
		allow[p] = struct{}{}
	}
	tlsCfg, err := tlsConfig(os.Getenv("TUNNEL_TLS_SHA256"))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("forwarding to 127.0.0.1 on ports %s", env("ALLOW_PORTS", "443,8443,1080,8081"))

	backoff := 3 * time.Second
	for {
		start := time.Now()
		err := run(addr, token, tlsCfg, allow)
		if err != nil {
			log.Printf("tunnel disconnected: %v", err)
		}
		if time.Since(start) > 30*time.Second {
			backoff = 3 * time.Second
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff += 3 * time.Second
		}
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func parsePorts(raw string) ([]uint16, error) {
	seen := make(map[uint16]struct{})
	var out []uint16
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid port %q", part)
		}
		p := uint16(n)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, errors.New("port list is empty")
	}
	return out, nil
}

func tlsConfig(pin string) (*tls.Config, error) {
	normalized, err := normalizePin(pin)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: "iran-tcp-tunnel",
	}
	if normalized == "insecure" {
		cfg.InsecureSkipVerify = true
		log.Printf("TLS certificate is not pinned; the tunnel connects anyway. VPN apps do not use this certificate")
		return cfg, nil
	}
	// Hostname/CA verification cannot succeed for the auto-generated certificate.
	// VerifyConnection still checks the exact certificate hash below.
	cfg.InsecureSkipVerify = true
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("iran server presented no certificate")
		}
		sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
		got := hex.EncodeToString(sum[:])
		if subtle.ConstantTimeCompare([]byte(got), []byte(normalized)) != 1 {
			return fmt.Errorf("TLS pin mismatch: server presented %s", got)
		}
		return nil
	}
	return cfg, nil
}

func normalizePin(pin string) (string, error) {
	pin = strings.ToLower(strings.TrimSpace(pin))
	pin = strings.TrimPrefix(pin, "tls_sha256=")
	pin = strings.TrimPrefix(pin, "sha256:")
	pin = strings.TrimPrefix(pin, "sha256/")
	pin = strings.ReplaceAll(pin, ":", "")
	pin = strings.ReplaceAll(pin, " ", "")
	if pin == "" || pin == "insecure" {
		return "insecure", nil
	}
	if len(pin) != 64 || strings.IndexFunc(pin, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	}) >= 0 {
		return "", errors.New("TUNNEL_TLS_SHA256 must be 64 hex characters, optionally prefixed with tls_sha256=, or the word insecure")
	}
	return pin, nil
}

func run(addr, token string, cfg *tls.Config, allow map[uint16]struct{}) error {
	dialer := net.Dialer{
		Timeout:         15 * time.Second,
		KeepAliveConfig: net.KeepAliveConfig{Enable: true, Interval: 30 * time.Second},
	}
	raw, err := dialer.Dial("tcp", addr)
	if err != nil {
		return err
	}
	conn := tls.Client(raw, cfg)
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if err := conn.Handshake(); err != nil {
		_ = raw.Close()
		return err
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(token)))
	if _, err := conn.Write(hdr[:]); err != nil {
		_ = conn.Close()
		return err
	}
	if _, err := conn.Write([]byte(token)); err != nil {
		_ = conn.Close()
		return err
	}
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil || ack[0] != 1 {
		_ = conn.Close()
		return errors.New("tunnel authentication rejected")
	}
	_ = conn.SetDeadline(time.Time{})

	muxCfg := yamux.DefaultConfig()
	muxCfg.EnableKeepAlive = true
	muxCfg.KeepAliveInterval = 30 * time.Second
	muxCfg.ConnectionWriteTimeout = 60 * time.Second
	muxCfg.StreamCloseTimeout = 0
	muxCfg.LogOutput = io.Discard
	mux, err := yamux.Client(conn, muxCfg)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer mux.Close()
	log.Printf("connected to %s", addr)
	for {
		stream, err := mux.Accept()
		if err != nil {
			return err
		}
		go handle(stream, allow)
	}
}

func handle(stream net.Conn, allow map[uint16]struct{}) {
	defer stream.Close()
	_ = stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	var hdr [2]byte
	if _, err := io.ReadFull(stream, hdr[:]); err != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	port := binary.BigEndian.Uint16(hdr[:])
	if _, ok := allow[port]; !ok {
		log.Printf("rejected forwarded port %d", port)
		return
	}
	dialer := net.Dialer{
		Timeout:         10 * time.Second,
		KeepAliveConfig: net.KeepAliveConfig{Enable: true, Interval: 30 * time.Second},
	}
	dst, err := dialer.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil {
		log.Printf("dial local port %d: %v", port, err)
		return
	}
	defer dst.Close()
	proxy(dst, stream)
}

func proxy(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		closeWrite(a)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		closeWrite(b)
	}()
	wg.Wait()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}
