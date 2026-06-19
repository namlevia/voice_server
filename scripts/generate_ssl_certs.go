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
	fmt.Println("🔐 Tạo chứng chỉ SSL...")

	// Tạo thư mục ssl
	sslDir := "../ssl"
	if err := os.MkdirAll(sslDir, 0755); err != nil {
		fmt.Printf("❌ Tạo thư mục SSL không thành công: %v\n", err)
		os.Exit(1)
	}

	// Tạo khóa riêng
	fmt.Println("🔑 Tạo khóa riêng ECDSA...")
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fmt.Printf("❌ Không tạo được khóa riêng: %v\n", err)
		os.Exit(1)
	}

	// Tạo mẫu chứng chỉ
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		fmt.Printf("❌ Không tạo được số seri: %v\n", err)
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
	fmt.Println("📜 Tạo chứng chỉ tự ký...")
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		fmt.Printf("❌ Tạo chứng chỉ không thành công: %v\n", err)
		os.Exit(1)
	}

	// lưu chứng chỉ
	certPath := filepath.Join(sslDir, "cert.pem")
	certOut, err := os.Create(certPath)
	if err != nil {
		fmt.Printf("❌ Không tạo được file chứng chỉ: %v\n", err)
		os.Exit(1)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		fmt.Printf("❌ Không ghi được dữ liệu chứng chỉ: %v\n", err)
		os.Exit(1)
	}
	if err := certOut.Close(); err != nil {
		fmt.Printf("❌ Đóng tệp chứng chỉ không thành công: %v\n", err)
		os.Exit(1)
	}

	// lưu khóa riêng
	keyPath := filepath.Join(sslDir, "key.pem")
	keyOut, err := os.Create(keyPath)
	if err != nil {
		fmt.Printf("❌ Không tạo được file khóa riêng: %v\n", err)
		os.Exit(1)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		fmt.Printf("❌ Không thể tuần tự hóa khóa riêng: %v\n", err)
		os.Exit(1)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		fmt.Printf("❌ Ghi dữ liệu khóa riêng không thành công: %v\n", err)
		os.Exit(1)
	}
	if err := keyOut.Close(); err != nil {
		fmt.Printf("❌ Không đóng được file khóa riêng: %v\n", err)
		os.Exit(1)
	}

	// Đặt quyền truy cập tệp (chỉ trên các hệ thống giống Unix)
	os.Chmod(keyPath, 0600)  // Khóa riêng chỉ có chủ sở hữu mới có thể đọc được
	os.Chmod(certPath, 0644) // Chứng chỉ có thể được đọc bởi người khác

	fmt.Println("✅ Chứng chỉ SSL được tạo thành công!")
	fmt.Printf("📁 Vị trí chứng chỉ: %s\n", sslDir)
	fmt.Printf("📜 Tệp chứng chỉ: %s\n", certPath)
	fmt.Printf("🔑 Tệp khóa riêng: %s\n", keyPath)
	fmt.Println("")
	fmt.Println("⚠️ LƯU Ý QUAN TRỌNG:")
	fmt.Println("- Đây là chứng chỉ tự ký và trình duyệt sẽ hiển thị cảnh báo bảo mật")
	fmt.Println("- Bạn cần chấp nhận chứng chỉ theo cách thủ công trong lần truy cập đầu tiên")
	fmt.Println("- Thời hạn hiệu lực của chứng chỉ: 365 ngày")
	fmt.Println("- Tên miền hỗ trợ: localhost, *.localhost")
	fmt.Println("- IP hỗ trợ: 127.0.0.1, ::1")
}
