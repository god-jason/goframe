package license

import (
	"context"
	"encoding/hex"
	"testing"
)

func TestCollectFingerprintHostID(t *testing.T) {
	info, err := CollectFingerprint(context.Background(), FingerprintOptions{Sources: []string{SourceHostID}})
	if err != nil {
		t.Skipf("host id unavailable: %v", err)
	}
	if info.Source != SourceHostID {
		t.Fatalf("source = %s", info.Source)
	}
	if _, err := hex.DecodeString(info.Fingerprint); err != nil || len(info.Fingerprint) != 64 {
		t.Fatalf("invalid fingerprint %q", info.Fingerprint)
	}
}

func TestCollectFingerprintPriority(t *testing.T) {
	sources := []string{SourceCPUID, SourceMAC}
	first, err := CollectFingerprint(context.Background(), FingerprintOptions{Sources: sources})
	if err != nil {
		t.Skipf("no fingerprint source available: %v", err)
	}
	if first.Source != SourceCPUID && first.Source != SourceMAC {
		t.Fatalf("unexpected source %q", first.Source)
	}

	second, err := CollectFingerprint(context.Background(), FingerprintOptions{Sources: sources})
	if err != nil {
		t.Fatalf("second collection: %v", err)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatal("fingerprint is not deterministic")
	}
}

func TestNormalizeUUID(t *testing.T) {
	tests := map[string]string{
		" 4c4c4544-0030-3010-8043-b4c04f4e4d32 \n":     "4C4C4544-0030-3010-8043-B4C04F4E4D32",
		"UUID\n4C4C4544-0030-3010-8043-B4C04F4E4D32\n": "4C4C4544-0030-3010-8043-B4C04F4E4D32",
		"00000000-0000-0000-0000-000000000000":         "",
		"03000200-0400-0500-0006-000700080009":         "",
		"not-a-uuid":                                   "",
	}
	for input, want := range tests {
		if got := normalizeUUID(input); got != want {
			t.Errorf("normalizeUUID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseIOPlatformUUID(t *testing.T) {
	output := `"IOPlatformUUID" = "12345678-1234-1234-1234-1234567890AB"`
	if got := parseIOPlatformUUID(output); got != "12345678-1234-1234-1234-1234567890AB" {
		t.Fatalf("uuid = %q", got)
	}
	if got := parseIOPlatformUUID("nothing"); got != "" {
		t.Fatalf("uuid = %q", got)
	}
}

func TestVirtualInterface(t *testing.T) {
	for _, name := range []string{"docker0", "vEthernet (Default Switch)", "br-1234", "utun3", "tailscale0"} {
		if !virtualInterface(name) {
			t.Errorf("%q should be virtual", name)
		}
	}
	for _, name := range []string{"eth0", "eno1", "en0", "Ethernet"} {
		if virtualInterface(name) {
			t.Errorf("%q should not be virtual", name)
		}
	}
}
