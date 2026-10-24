package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// TLSConfig 面板/节点通信的传输加密。
// 默认自签证书 + SHA256 指纹固定：被控机通过 -tls-fingerprint 校验主控证书，
// 无需 CA 体系也能防中间人（指纹在主控安装输出/节点管理页展示）。
type TLSConfig struct {
	Enabled     bool
	CertFile    string
	KeyFile     string
	Fingerprint string //SHA256 hex（自签证书的指纹）
	Generated   bool   //是否为本面板自动生成
}

func sha256Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// localIPs 收集本机所有 IP，写进证书 SAN，方便直接用任意本机 IP 访问。
func localIPs() []net.IP {
	var out []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil {
				out = append(out, ip)
			}
		}
	}
	return out
}

// GenerateSelfSigned 生成 10 年期 ECDSA P-256 自签证书并落盘。
func GenerateSelfSigned(certFile, keyFile string) (*TLSConfig, error) {
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return nil, err
	}

	host, _ := os.Hostname()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "relay-panel", Organization: []string{"relay-panel"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	//SAN：主机名 + 本机全部 IP + 常用回环
	dnsNames := []string{host, "localhost"}
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	ips = append(ips, localIPs()...)
	tmpl.DNSNames = dnsNames
	tmpl.IPAddresses = ips

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writeFileAtomic(certFile, certPEM, 0o644); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(keyFile, keyPEM, 0o600); err != nil {
		return nil, err
	}
	fp := sha256Fingerprint(der)
	//指纹单独落盘，安装脚本/文档引用
	_ = writeFileAtomic(filepath.Join(filepath.Dir(certFile), "fingerprint"), []byte(fp+"\n"), 0o644)

	return &TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile, Fingerprint: fp, Generated: true}, nil
}

// LoadTLS 按配置准备面板 TLS：外部证书优先，否则自动生成自签。
// tlsMode: "0" 关闭；"1" 开启（默认）。返回 nil 表示明文 HTTP。
func LoadTLS(tlsMode, certFile, keyFile, stateDir string) (*TLSConfig, error) {
	if tlsMode == "0" || tlsMode == "" {
		return nil, nil
	}
	dir := filepath.Join(stateDir, "tls")
	defCert := filepath.Join(dir, "server.crt")
	defKey := filepath.Join(dir, "server.key")

	if certFile != "" && keyFile != "" {
		if _, err := os.Stat(certFile); err != nil {
			return nil, fmt.Errorf("证书文件不存在: %s", certFile)
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("加载证书失败: %w", err)
		}
		return &TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile,
			Fingerprint: sha256Fingerprint(cert.Certificate[0]), Generated: false}, nil
	}

	//已有自签证书直接复用（重启不换证书，指纹保持稳定）
	if _, err := os.Stat(defCert); err == nil {
		if _, err2 := os.Stat(defKey); err2 == nil {
			cert, err := tls.LoadX509KeyPair(defCert, defKey)
			if err == nil {
				return &TLSConfig{Enabled: true, CertFile: defCert, KeyFile: defKey,
					Fingerprint: sha256Fingerprint(cert.Certificate[0]), Generated: true}, nil
			}
		}
	}
	return GenerateSelfSigned(defCert, defKey)
}

// SlaveTLSClient 被控机的 TLS 校验策略：优先指纹固定；未给指纹则退化为不校验（明示风险）。
func SlaveTLSClient(fingerprint string, insecure bool) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if fingerprint != "" {
		want := fingerprint
		cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			for _, raw := range rawCerts {
				if sha256Fingerprint(raw) == want {
					return nil
				}
			}
			return fmt.Errorf("主控证书指纹不匹配（期望 %s）", want)
		}
		//指纹固定时跳过系统 CA 校验（自签场景），安全性由指纹保证
		cfg.InsecureSkipVerify = true
		return cfg
	}
	if insecure {
		cfg.InsecureSkipVerify = true
	}
	return cfg
}
