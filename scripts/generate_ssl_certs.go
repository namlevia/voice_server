package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	fmt.Println("🔐 生成SSL证书...")

	// Tạo thư mục ssl
	sslDir := "../ssl"
	if err := os.MkdirAll(sslDir, 0755); err != nil {
		fmt.Printf("❌ 创建SSL目录失败: %v\n", err)
		os.Exit(1)
	}

	// Tạo khóa riêng
	fmt.Println("🔑 生成ECDSA私钥...")
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fmt.Printf("❌ 生成私钥失败: %v\n", err)
		os.Exit(1)
	}

	// Tạo mẫu chứng chỉ
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		fmt.Printf("❌ 生成序列号失败: %v\n", err)
		os.Exit(1)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization:  []string{"VAD ASR Server"},
			Country:       []string{"CN"},
			Province:      []string{"Beijing"},
			Locality:      []string{"Beijing"},
			StreetAddress: []string{},
			PostalCode:    []string{},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // hiệu lực 1 năm
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost", "*.localhost"},
	}

	// Tạo chứng chỉ
	fmt.Println("📜 生成自签名证书...")
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		fmt.Printf("❌ 创建证书失败: %v\n", err)
		os.Exit(1)
	}

	// lưu chứng chỉ
	certPath := filepath.Join(sslDir, "cert.pem")
	certOut, err := os.Create(certPath)
	if err != nil {
		fmt.Printf("❌ 创建证书文件失败: %v\n", err)
		os.Exit(1)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		fmt.Printf("❌ 写入证书数据失败: %v\n", err)
		os.Exit(1)
	}
	if err := certOut.Close(); err != nil {
		fmt.Printf("❌ 关闭证书文件失败: %v\n", err)
		os.Exit(1)
	}

	// lưu khóa riêng
	keyPath := filepath.Join(sslDir, "key.pem")
	keyOut, err := os.Create(keyPath)
	if err != nil {
		fmt.Printf("❌ 创建私钥文件失败: %v\n", err)
		os.Exit(1)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		fmt.Printf("❌ 序列化私钥失败: %v\n", err)
		os.Exit(1)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		fmt.Printf("❌ 写入私钥数据失败: %v\n", err)
		os.Exit(1)
	}
	if err := keyOut.Close(); err != nil {
		fmt.Printf("❌ 关闭私钥文件失败: %v\n", err)
		os.Exit(1)
	}

	// Đặt quyền truy cập tệp (chỉ trên các hệ thống giống Unix)
	os.Chmod(keyPath, 0600)  // Khóa riêng chỉ có chủ sở hữu mới có thể đọc được
	os.Chmod(certPath, 0644) // Chứng chỉ có thể được đọc bởi người khác

	fmt.Println("✅ SSL证书生成成功!")
	fmt.Printf("📁 证书位置: %s\n", sslDir)
	fmt.Printf("📜 证书文件: %s\n", certPath)
	fmt.Printf("🔑 私钥文件: %s\n", keyPath)
	fmt.Println("")
	fmt.Println("⚠️  重要提示:")
	fmt.Println("  - 这是自签名证书，浏览器会显示安全警告")
	fmt.Println("  - 首次访问时需要手动接受证书")
	fmt.Println("  - 证书有效期: 365天")
	fmt.Println("  - 支持域名: localhost, *.localhost")
	fmt.Println("  - 支持IP: 127.0.0.1, ::1")
}
