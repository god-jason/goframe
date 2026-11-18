package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RevocationList 是离线吊销列表，由同一私钥签发。
type RevocationList struct {
	GeneratedAt time.Time         `json:"generated_at"`
	IDs         []string          `json:"ids"`
	Reasons     map[string]string `json:"reasons,omitempty"`
}

// Contains 判断许可 ID 是否已被吊销。
func (r *RevocationList) Contains(id string) bool {
	if r == nil {
		return false
	}
	id = strings.TrimSpace(id)
	for _, item := range r.IDs {
		if item == id {
			return true
		}
	}
	return false
}

// SignRevocation 签发吊销列表。
func SignRevocation(list *RevocationList, privateKey ed25519.PrivateKey) ([]byte, error) {
	if list == nil {
		return nil, fmt.Errorf("license: revocation list is nil")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("license: invalid private key")
	}
	if list.GeneratedAt.IsZero() {
		list.GeneratedAt = time.Now()
	}

	payload, err := json.Marshal(list)
	if err != nil {
		return nil, fmt.Errorf("license: encode revocation list: %w", err)
	}
	signature := ed25519.Sign(privateKey, payload)

	content, err := json.MarshalIndent(envelope{
		Version:   envelopeVersion,
		Payload:   base64.StdEncoding.EncodeToString(payload),
		Signature: base64.StdEncoding.EncodeToString(signature),
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("license: encode revocation envelope: %w", err)
	}
	return content, nil
}

// VerifyRevocation 校验并解析吊销列表。
func VerifyRevocation(data []byte, publicKey ed25519.PublicKey) (*RevocationList, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license: invalid public key")
	}

	var file envelope
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("license: decode revocation file: %w", err)
	}
	if file.Version != envelopeVersion {
		return nil, fmt.Errorf("license: unsupported envelope version %d", file.Version)
	}
	payload, err := base64.StdEncoding.DecodeString(file.Payload)
	if err != nil {
		return nil, fmt.Errorf("license: decode revocation payload: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(file.Signature)
	if err != nil {
		return nil, fmt.Errorf("license: decode revocation signature: %w", err)
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return nil, fmt.Errorf("license: invalid revocation signature")
	}

	var list RevocationList
	if err := json.Unmarshal(payload, &list); err != nil {
		return nil, fmt.Errorf("license: decode revocation list: %w", err)
	}
	return &list, nil
}
