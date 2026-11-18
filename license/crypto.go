package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"
)

// envelopeVersion 是许可信封格式版本。
const envelopeVersion = 1

// envelope 是许可文件的签名信封。Payload 为原始 JSON 字节的 base64。
type envelope struct {
	Version   int    `json:"version"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// IssueOptions 是签发许可的参数。
type IssueOptions struct {
	ID          string
	Product     string
	Customer    string
	Edition     string
	IssuedAt    time.Time
	NotBefore   *time.Time
	ExpiresAt   *time.Time
	GracePeriod time.Duration
	Features    []string
	Limits      map[string]int64
	Machines    []string
	Metadata    map[string]string
}

// GenerateKeyPair 生成 Ed25519 密钥对，返回 PEM 编码的公钥与私钥。
func GenerateKeyPair() (publicPEM, privatePEM []byte, err error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("license: generate key: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("license: marshal public key: %w", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("license: marshal private key: %w", err)
	}

	publicPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	privatePEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	return publicPEM, privatePEM, nil
}

// ParsePublicKeyPEM 解析 PEM 编码的 Ed25519 公钥。
func ParsePublicKeyPEM(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("license: invalid public key PEM")
	}
	value, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("license: parse public key: %w", err)
	}
	publicKey, ok := value.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("license: public key must be Ed25519")
	}
	return publicKey, nil
}

// ParsePrivateKeyPEM 解析 PEM 编码的 Ed25519 私钥。
func ParsePrivateKeyPEM(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("license: invalid private key PEM")
	}
	value, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("license: parse private key: %w", err)
	}
	privateKey, ok := value.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("license: private key must be Ed25519")
	}
	return privateKey, nil
}

// SignLicense 校验并签发许可，返回可直接下发的许可文件内容。
func SignLicense(license *License, privateKey ed25519.PrivateKey) ([]byte, error) {
	if license == nil {
		return nil, fmt.Errorf("license: license is nil")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("license: invalid private key")
	}
	if err := license.validate(); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(license)
	if err != nil {
		return nil, fmt.Errorf("license: encode license: %w", err)
	}
	signature := ed25519.Sign(privateKey, payload)

	content, err := json.MarshalIndent(envelope{
		Version:   envelopeVersion,
		Payload:   base64.StdEncoding.EncodeToString(payload),
		Signature: base64.StdEncoding.EncodeToString(signature),
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("license: encode envelope: %w", err)
	}
	return content, nil
}

// VerifyLicense 校验许可文件签名并解析许可内容。
func VerifyLicense(data []byte, publicKey ed25519.PublicKey) (*License, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license: invalid public key")
	}

	var file envelope
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("license: decode license file: %w", err)
	}
	if file.Version != envelopeVersion {
		return nil, fmt.Errorf("license: unsupported envelope version %d", file.Version)
	}

	payload, err := base64.StdEncoding.DecodeString(file.Payload)
	if err != nil {
		return nil, fmt.Errorf("license: decode payload: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(file.Signature)
	if err != nil {
		return nil, fmt.Errorf("license: decode signature: %w", err)
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return nil, fmt.Errorf("license: invalid license signature")
	}

	var license License
	if err := json.Unmarshal(payload, &license); err != nil {
		return nil, fmt.Errorf("license: decode license: %w", err)
	}
	if err := license.validate(); err != nil {
		return nil, err
	}
	return &license, nil
}

// Issue 构造并签发许可。
func Issue(options IssueOptions, privateKey ed25519.PrivateKey) ([]byte, *License, error) {
	issuedAt := options.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = time.Now()
	}
	license := &License{
		ID:                 options.ID,
		Product:            options.Product,
		Customer:           options.Customer,
		Edition:            options.Edition,
		IssuedAt:           issuedAt,
		NotBefore:          options.NotBefore,
		ExpiresAt:          options.ExpiresAt,
		GracePeriodSeconds: int64(options.GracePeriod / time.Second),
		Features:           append([]string(nil), options.Features...),
		Machines:           append([]string(nil), options.Machines...),
	}
	if options.Limits != nil {
		license.Limits = make(map[string]int64, len(options.Limits))
		for key, value := range options.Limits {
			license.Limits[key] = value
		}
	}
	if options.Metadata != nil {
		license.Metadata = make(map[string]string, len(options.Metadata))
		for key, value := range options.Metadata {
			license.Metadata[key] = value
		}
	}

	content, err := SignLicense(license, privateKey)
	if err != nil {
		return nil, nil, err
	}
	return content, license, nil
}
