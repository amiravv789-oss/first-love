package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	token := os.Getenv("TUNNEL_TOKEN")
	if len(token) < 32 || len(token) > 512 {
		log.Fatal("TUNNEL_TOKEN must be 32 to 512 characters")
	}
	ports, err := parsePorts(env("PUBLIC_PORTS", "443,8443,1080,8081"))
	if err != nil {
		log.Fatalf("PUBLIC_PORTS: %v", err)
	}
	if len(ports) > 64 {
		log.Fatal("PUBLIC_PORTS contains too many ports")
	}

	cert, err := loadOrCreateCert(env("TLS_DIR", "/data"))
	if err != nil {
		log.Fatalf("TLS certificate: %v", err)
	}
	sum := sha256.Sum256(cert.Certificate[0])
	log.Printf("tls_sha256=%s", hex.EncodeToString(sum[:]))
	log.Printf("VPN clients do not use this certificate; leave TUNNEL_TLS_SHA256 empty unless you want to pin it")

	rawLn, err := listenTCP(env("LISTEN_ADDR", ":8787"))
	if err != nil {
		log.Fatalf("listen tunnel control: %v", err)
	}
	tlsLn := tls.NewListener(rawLn, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	log.Printf("tunnel control listening on %s", tlsLn.Addr())

	hub := &sessions{}
	go acceptControl(tlsLn, token, hub)

	for _, port := range ports {
		ln, err := listenTCP(publicAddr(port))
		if err != nil {
			log.Fatalf("listen client port %d: %v", port, err)
		}
		log.Printf("public TCP listener on %s", ln.Addr())
		go acceptClients(ln, port, hub)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
	os.Exit(0)
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
			return nil, errors.New("invalid port " + strconv.Quote(part))
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

func publicAddr(port uint16) string {
	host := strings.TrimSpace(os.Getenv("BIND_HOST"))
	if host == "" {
		return ":" + strconv.Itoa(int(port))
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

func listenTCP(addr string) (net.Listener, error) {
	lc := net.ListenConfig{KeepAliveConfig: net.KeepAliveConfig{Enable: true, Interval: 30 * time.Second}}
	return lc.Listen(context.Background(), "tcp", addr)
}

type sessions struct {
	mu  sync.RWMutex
	cur *yamux.Session
}

func (s *sessions) replace(next *yamux.Session) {
	s.mu.Lock()
	old := s.cur
	s.cur = next
	s.mu.Unlock()
	if old != nil && old != next {
		_ = old.Close()
	}
}

func (s *sessions) clear(sess *yamux.Session) {
	s.mu.Lock()
	if s.cur == sess {
		s.cur = nil
	}
	s.mu.Unlock()
}

func (s *sessions) get() *yamux.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func acceptControl(ln net.Listener, token string, hub *sessions) {
	sem := make(chan struct{}, 16)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Public port: bad TLS handshakes show up here. Don't stall the listener.
			time.Sleep(200 * time.Millisecond)
			continue
		}
		select {
		case sem <- struct{}{}:
			go func(c net.Conn) {
				defer func() { <-sem }()
				handleControl(c, token, hub)
			}(conn)
		default:
			_ = conn.Close()
		}
	}
}

func handleControl(c net.Conn, token string, hub *sessions) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	var hdr [2]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n < 32 || n > 512 {
		log.Printf("rejected tunnel control from %s", c.RemoteAddr())
		return
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		return
	}
	tokenBytes := []byte(token)
	ok := len(buf) == len(tokenBytes) && subtle.ConstantTimeCompare(buf, tokenBytes) == 1
	if !ok {
		log.Printf("rejected tunnel control from %s", c.RemoteAddr())
		time.Sleep(500 * time.Millisecond)
		return
	}
	if _, err := c.Write([]byte{1}); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})

	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second
	cfg.ConnectionWriteTimeout = 60 * time.Second
	cfg.StreamCloseTimeout = 0
	cfg.LogOutput = io.Discard
	sess, err := yamux.Server(c, cfg)
	if err != nil {
		log.Printf("yamux server: %v", err)
		return
	}
	hub.replace(sess)
	log.Printf("tunnel agent connected from %s", c.RemoteAddr())
	<-sess.CloseChan()
	hub.clear(sess)
	log.Printf("tunnel agent disconnected")
}

func acceptClients(ln net.Listener, port uint16, hub *sessions) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("accept port %d: %v", port, err)
			time.Sleep(time.Second)
			continue
		}
		go serveClient(conn, port, hub)
	}
}

func serveClient(client net.Conn, port uint16, hub *sessions) {
	defer client.Close()
	sess := hub.get()
	if sess == nil || sess.IsClosed() {
		return
	}
	stream, err := sess.Open()
	if err != nil {
		return
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(15 * time.Second))
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], port)
	if _, err := stream.Write(hdr[:]); err != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	proxy(stream, client)
}

// proxy copies both directions until each one reaches EOF.
// Closing everything when the first direction finishes drops HTTP, SOCKS and
// VLESS responses, because the request side often ends before the reply.
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

// TCPConn implements CloseWrite. A yamux stream does not; its first Close sends
// FIN and still allows reads, which is the half-close this proxy needs.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

func loadOrCreateCert(dir string) (tls.Certificate, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	_ = os.Chmod(dir, 0o700)
	certPath := filepath.Join(dir, "tls-cert.pem")
	keyPath := filepath.Join(dir, "tls-key.pem")
	if st, err := os.Stat(certPath); err == nil && !st.IsDir() {
		if st, err := os.Stat(keyPath); err == nil && !st.IsDir() {
			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err == nil && len(cert.Certificate) > 0 {
				return cert, nil
			}
			log.Printf("replacing unreadable TLS certificate in %s: %v", dir, err)
		}
	}
	cert, err := generateCert(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	log.Printf("generated a new self-signed TLS certificate in %s", dir)
	return cert, nil
}

func generateCert(certPath, keyPath string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	serial.Add(serial, big.NewInt(1))
	dnsNames, ips := certNames()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "iran-tcp-tunnel"},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := writePEM(certPath, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		return tls.Certificate{}, err
	}
	if err := writePEM(keyPath, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(certPath, keyPath)
}

func certNames() ([]string, []net.IP) {
	dns := []string{"localhost", "iran-tcp-tunnel"}
	ips := []net.IP{net.IPv4(127, 0, 0, 1)}
	seenDNS := map[string]struct{}{"localhost": {}, "iran-tcp-tunnel": {}}
	seenIP := map[string]struct{}{"127.0.0.1": {}}
	for _, part := range strings.Split(os.Getenv("TLS_HOSTNAMES"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ip := net.ParseIP(part); ip != nil {
			if _, ok := seenIP[ip.String()]; ok {
				continue
			}
			seenIP[ip.String()] = struct{}{}
			ips = append(ips, ip)
			continue
		}
		low := strings.ToLower(part)
		if _, ok := seenDNS[low]; ok {
			continue
		}
		seenDNS[low] = struct{}{}
		dns = append(dns, part)
	}
	return dns, ips
}

func writePEM(path string, block *pem.Block) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = pem.Encode(f, block)
	if err2 := f.Close(); err == nil {
		err = err2
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
