package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"io"
	"log"
	"net"
	"os"
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
	tlsCfg, err := tlsConfig(os.Getenv("TUNNEL_TLS_SHA256"))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("exit-node agent connecting to %s", addr)

	backoff := 3 * time.Second
	for {
		start := time.Now()
		err := run(addr, token, tlsCfg)
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

func tlsConfig(pin string) (*tls.Config, error) {
	normalized := strings.TrimSpace(strings.ToLower(pin))
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: "iran-tcp-tunnel",
	}
	if normalized == "" || normalized == "insecure" {
		cfg.InsecureSkipVerify = true
		if normalized == "insecure" {
			log.Printf("TLS certificate verification disabled")
		}
		return cfg, nil
	}
	want, err := hex.DecodeString(normalized)
	if err != nil || len(want) != 32 {
		return nil, errors.New("TUNNEL_TLS_SHA256 must be 64 hex chars or empty/insecure")
	}
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*tls.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("no certificate")
		}
		sum := sha256.Sum256(rawCerts[0])
		if subtle.ConstantTimeCompare(sum[:], want) != 1 {
			return errors.New("certificate pin mismatch")
		}
		return nil
	}
	cfg.InsecureSkipVerify = true
	return cfg, nil
}

func run(addr, token string, cfg *tls.Config) error {
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
	log.Printf("connected to %s (exit node ready)", addr)
	for {
		stream, err := mux.Accept()
		if err != nil {
			return err
		}
		go handleStream(stream)
	}
}

func handleStream(stream net.Conn) {
	defer stream.Close()
	_ = stream.SetReadDeadline(time.Now().Add(20 * time.Second))

	// Protocol: 1 byte hostLen + host + 2 byte port (big endian)
	var lenb [1]byte
	if _, err := io.ReadFull(stream, lenb[:]); err != nil {
		return
	}
	hostLen := int(lenb[0])
	if hostLen < 1 || hostLen > 253 {
		return
	}
	hostBuf := make([]byte, hostLen)
	if _, err := io.ReadFull(stream, hostBuf); err != nil {
		return
	}
	var portb [2]byte
	if _, err := io.ReadFull(stream, portb[:]); err != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	port := binary.BigEndian.Uint16(portb[:])
	host := string(hostBuf)
	target := net.JoinHostPort(host, itoa(int(port)))

	dialer := net.Dialer{
		Timeout:         15 * time.Second,
		KeepAliveConfig: net.KeepAliveConfig{Enable: true, Interval: 30 * time.Second},
	}
	dst, err := dialer.Dial("tcp", target)
	if err != nil {
		log.Printf("dial %s: %v", target, err)
		return
	}
	defer dst.Close()
	proxy(dst, stream)
}

func itoa(n int) string {
	return strconv.Itoa(n)
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
