// Package kerneltest holds helpers for runtime tests that start real kernels
// and drive real proxy clients through them. It is only imported by tests.
package kerneltest

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	singJSON "github.com/sagernet/sing/common/json"
	singM "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/cedar2025/xboard-node/internal/kernel"
)

// EnvRuntime gates tests that bind real ports.
const EnvRuntime = "XBOARD_RUNTIME_TEST"

// RequireRuntime skips the test unless XBOARD_RUNTIME_TEST is set.
func RequireRuntime(t *testing.T) {
	t.Helper()
	if os.Getenv(EnvRuntime) == "" {
		t.Skip("set " + EnvRuntime + "=1 to run (starts real listeners)")
	}
}

// SelfSignedCert returns a throwaway certificate for "localhost".
func SelfSignedCert(t *testing.T) kernel.TLSCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return kernel.TLSCert{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
}

// FreePort returns a currently unused local TCP port.
func FreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// StartEcho runs a TCP echo server on 127.0.0.1 (and ::1 on the same port when
// IPv6 is available, so "localhost" works either way) and returns its port.
func StartEcho(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	serveEcho(t, l)
	if l6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", port)); err == nil {
		serveEcho(t, l6)
	}
	return port
}

func serveEcho(t *testing.T, l net.Listener) {
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
}

// RoundTrip starts a fresh sing-box client with the given outbound (so every
// call performs a new handshake and authentication), connects through it to
// host:port and checks that a payload is echoed back unchanged.
func RoundTrip(outbound map[string]any, host string, port int) error {
	outbound["tag"] = "proxy"
	data, err := json.Marshal(map[string]any{
		"log":       map[string]any{"level": "panic"},
		"outbounds": []any{outbound},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	defer cancel()
	opts, err := singJSON.UnmarshalExtendedContext[option.Options](ctx, data)
	if err != nil {
		return fmt.Errorf("client options: %w", err)
	}
	instance, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		return fmt.Errorf("client create: %w", err)
	}
	defer instance.Close()
	if err = instance.Start(); err != nil {
		return fmt.Errorf("client start: %w", err)
	}
	out, ok := service.FromContext[adapter.OutboundManager](ctx).Outbound("proxy")
	if !ok {
		return fmt.Errorf("client outbound missing")
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 3*time.Second)
	defer dialCancel()
	conn, err := out.DialContext(dialCtx, N.NetworkTCP, singM.ParseSocksaddrHostPort(host, uint16(port)))
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("xboard-e2e-" + time.Now().String())
	if _, err = conn.Write(payload); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	reply := make([]byte, len(payload))
	if _, err = io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if !bytes.Equal(reply, payload) {
		return fmt.Errorf("echo mismatch")
	}
	return nil
}

// StartUDPEcho runs a UDP echo server on 127.0.0.1 and returns its port.
func StartUDPEcho(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

// UDPProbe opens one UDP session through the outbound, first sending a packet
// to an allowed public destination (so the session is routed as public), then
// a packet to target in the same session. It reports whether target answered.
func UDPProbe(outbound map[string]any, first, target singM.Socksaddr) (bool, error) {
	outbound["tag"] = "proxy"
	data, err := json.Marshal(map[string]any{
		"log":       map[string]any{"level": "panic"},
		"outbounds": []any{outbound},
	})
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	defer cancel()
	opts, err := singJSON.UnmarshalExtendedContext[option.Options](ctx, data)
	if err != nil {
		return false, err
	}
	instance, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		return false, err
	}
	defer instance.Close()
	if err = instance.Start(); err != nil {
		return false, err
	}
	out, _ := service.FromContext[adapter.OutboundManager](ctx).Outbound("proxy")
	pc, err := out.ListenPacket(ctx, first)
	if err != nil {
		return false, err
	}
	defer pc.Close()
	payload := []byte("xboard-udp-probe")
	if _, err = pc.WriteTo(payload, first.UDPAddr()); err != nil {
		return false, err
	}
	time.Sleep(100 * time.Millisecond)
	if _, err = pc.WriteTo(payload, target.UDPAddr()); err != nil {
		return false, err
	}
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return false, nil // timeout: no answer from target
		}
		if bytes.Equal(buf[:n], payload) {
			return true, nil
		}
	}
}
