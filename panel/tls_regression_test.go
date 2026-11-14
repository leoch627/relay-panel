package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pinnedPull 模拟被控机带指纹固定访问主控，返回请求是否到达了服务端。
func pinnedPull(t *testing.T, serverCert tls.Certificate, fingerprint string) (reached bool, err error) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		_, _ = w.Write([]byte(`{}`))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}}
	srv.StartTLS()
	defer srv.Close()

	transport := &http.Transport{TLSClientConfig: SlaveTLSClient(fingerprint, false)}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequest("POST", srv.URL+"/api/node/pull", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer node-key")
	resp, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return reached, err
	}
	resp.Body.Close()
	return reached, nil
}

// selfSignedForTest 生成一张与主控无关的自签证书（攻击者自己的密钥）。
func selfSignedForTest(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "not-the-master"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func masterCertForTest(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := GenerateSelfSigned(filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	return cert, cfg.Fingerprint
}

func TestPinnedFingerprintAcceptsMaster(t *testing.T) {
	master, fingerprint := masterCertForTest(t)
	reached, err := pinnedPull(t, master, fingerprint)
	if err != nil || !reached {
		t.Fatalf("pinned master rejected: reached=%v err=%v", reached, err)
	}
}

func TestPinnedFingerprintRejectsOtherCertificate(t *testing.T) {
	_, fingerprint := masterCertForTest(t)
	reached, err := pinnedPull(t, selfSignedForTest(t), fingerprint)
	if err == nil || reached {
		t.Fatalf("unrelated certificate accepted: reached=%v err=%v", reached, err)
	}
}

// 主控证书是公开的：中间人不持有其私钥，但可以把它附在自己的叶子证书后面。
// 指纹固定必须只认叶子证书，否则节点密钥会被发给冒充的主控。
func TestPinnedFingerprintIgnoresAppendedMasterCertificate(t *testing.T) {
	master, fingerprint := masterCertForTest(t)
	attacker := selfSignedForTest(t)
	attacker.Certificate = append(attacker.Certificate, master.Certificate[0])

	reached, err := pinnedPull(t, attacker, fingerprint)
	if err == nil || reached {
		t.Fatalf("pin bypassed by appending the master certificate: reached=%v err=%v", reached, err)
	}
	if !strings.Contains(err.Error(), "指纹不匹配") {
		t.Fatalf("expected a fingerprint mismatch, got: %v", err)
	}
}
