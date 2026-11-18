package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func testKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return publicKey, privateKey
}

func testIssueOptions() IssueOptions {
	now := time.Now()
	expires := now.Add(24 * time.Hour)
	return IssueOptions{
		ID:          "L-100",
		Product:     "goframe",
		Customer:    "ACME",
		Edition:     "enterprise",
		IssuedAt:    now,
		ExpiresAt:   &expires,
		GracePeriod: 48 * time.Hour,
		Features:    []string{"sso", "audit"},
		Limits:      map[string]int64{"users": 100, "devices": Unlimited},
		Machines:    []string{"fp-1"},
		Metadata:    map[string]string{"order": "PO-1"},
	}
}

func TestKeyPairPEMRoundTrip(t *testing.T) {
	publicPEM, privatePEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	publicKey, err := ParsePublicKeyPEM(publicPEM)
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}
	privateKey, err := ParsePrivateKeyPEM(privatePEM)
	if err != nil {
		t.Fatalf("ParsePrivateKeyPEM: %v", err)
	}
	if !publicKey.Equal(privateKey.Public()) {
		t.Fatal("public key mismatch")
	}
}

func TestSignAndVerifyLicense(t *testing.T) {
	publicKey, privateKey := testKeyPair(t)
	content, issued, err := Issue(testIssueOptions(), privateKey)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	verified, err := VerifyLicense(content, publicKey)
	if err != nil {
		t.Fatalf("VerifyLicense: %v", err)
	}
	if verified.ID != issued.ID || verified.Customer != "ACME" || len(verified.Features) != 2 {
		t.Fatalf("unexpected license: %+v", verified)
	}

	otherPublic, _ := testKeyPair(t)
	if _, err := VerifyLicense(content, otherPublic); err == nil {
		t.Fatal("expected signature verification failure")
	}

	tampered := tamperPayload(t, content)
	if _, err := VerifyLicense(tampered, publicKey); err == nil {
		t.Fatal("expected tampered license to fail")
	}
}

func TestSignLicenseValidation(t *testing.T) {
	_, privateKey := testKeyPair(t)
	if _, err := SignLicense(&License{}, privateKey); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRevocationList(t *testing.T) {
	publicKey, privateKey := testKeyPair(t)
	list := &RevocationList{IDs: []string{"L-100", "L-101"}}
	content, err := SignRevocation(list, privateKey)
	if err != nil {
		t.Fatalf("SignRevocation: %v", err)
	}
	verified, err := VerifyRevocation(content, publicKey)
	if err != nil {
		t.Fatalf("VerifyRevocation: %v", err)
	}
	if !verified.Contains("L-100") || verified.Contains("L-102") {
		t.Fatalf("unexpected revocation list: %+v", verified)
	}

	tampered := tamperPayload(t, content)
	if _, err := VerifyRevocation(tampered, publicKey); err == nil {
		t.Fatal("expected tampered revocation list to fail")
	}
}

// tamperPayload 修改信封中的载荷并保留原签名，用于验证签名校验。
func tamperPayload(t *testing.T, content []byte) []byte {
	t.Helper()
	var file envelope
	if err := json.Unmarshal(content, &file); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	payload, err := base64.StdEncoding.DecodeString(file.Payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	payload[len(payload)/2] ^= 0xff
	file.Payload = base64.StdEncoding.EncodeToString(payload)

	result, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	return result
}
