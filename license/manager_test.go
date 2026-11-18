package license

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestManager(t *testing.T, at time.Time, options IssueOptions, mutate func(*Options)) *Manager {
	t.Helper()
	publicKey, privateKey := testKeyPair(t)
	content, _, err := Issue(options, privateKey)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	opts := Options{
		PublicKey:   publicKey,
		LicenseData: content,
		Fingerprint: "fp-1",
		Now:         func() time.Time { return at },
	}
	if mutate != nil {
		mutate(&opts)
	}
	manager, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return manager
}

func TestManagerValidFeaturesAndLimits(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	manager := newTestManager(t, now, IssueOptions{
		ID:        "L-1",
		Product:   "goframe",
		IssuedAt:  now,
		ExpiresAt: &expires,
		Features:  []string{"sso", "audit"},
		Limits:    map[string]int64{"users": 10, "devices": Unlimited},
		Machines:  []string{"fp-1"},
	}, nil)

	if manager.Status() != StatusValid || !manager.Valid() || manager.InGrace() {
		t.Fatalf("status = %s", manager.Status())
	}
	if !manager.HasFeature("sso") || manager.HasFeature("billing") {
		t.Fatal("feature check failed")
	}
	if limit, ok := manager.Limit("users"); !ok || limit != 10 {
		t.Fatalf("users limit = %d, %v", limit, ok)
	}
	if !manager.WithinLimit("users", 9) || manager.WithinLimit("users", 10) {
		t.Fatal("limit boundary check failed")
	}
	if !manager.WithinLimit("devices", 1000) || !manager.WithinLimit("unknown", 1000) {
		t.Fatal("unlimited check failed")
	}
	snapshot := manager.Snapshot()
	if !snapshot.Valid || snapshot.License == nil || snapshot.License.ID != "L-1" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestManagerGraceAndExpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	graceExpires := now.Add(-time.Hour)
	grace := newTestManager(t, now, IssueOptions{
		ID:          "L-grace",
		Product:     "goframe",
		IssuedAt:    now.Add(-48 * time.Hour),
		ExpiresAt:   &graceExpires,
		GracePeriod: 2 * time.Hour,
		Features:    []string{"sso"},
	}, nil)
	if grace.Status() != StatusGrace || !grace.Valid() || !grace.InGrace() {
		t.Fatalf("grace status = %s", grace.Status())
	}
	if !grace.HasFeature("sso") {
		t.Fatal("features must remain available in grace period")
	}

	expiredAt := now.Add(-3 * time.Hour)
	expired := newTestManager(t, now, IssueOptions{
		ID:          "L-expired",
		Product:     "goframe",
		IssuedAt:    now.Add(-48 * time.Hour),
		ExpiresAt:   &expiredAt,
		GracePeriod: 2 * time.Hour,
		Features:    []string{"sso"},
	}, nil)
	if expired.Status() != StatusExpired || expired.Valid() || expired.HasFeature("sso") {
		t.Fatalf("expired status = %s", expired.Status())
	}
	if _, ok := expired.Limit("users"); ok {
		t.Fatal("limits must be unavailable when expired")
	}
}

func TestManagerNotYetValid(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notBefore := now.Add(time.Hour)
	manager := newTestManager(t, now, IssueOptions{
		ID:        "L-future",
		Product:   "goframe",
		IssuedAt:  now,
		NotBefore: &notBefore,
	}, nil)
	if manager.Status() != StatusNotYetValid || manager.Valid() {
		t.Fatalf("status = %s", manager.Status())
	}
}

func TestManagerMachineAndRevocation(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	options := IssueOptions{
		ID:       "L-2",
		Product:  "goframe",
		IssuedAt: now,
		Machines: []string{"fp-other"},
	}
	manager := newTestManager(t, now, options, func(opts *Options) {
		opts.Fingerprint = "fp-1"
	})
	if manager.Status() != StatusMachineMismatch || manager.Valid() {
		t.Fatalf("status = %s", manager.Status())
	}

	bound := newTestManager(t, now, IssueOptions{
		ID:       "L-3",
		Product:  "goframe",
		IssuedAt: now,
		Machines: []string{"*"},
	}, func(opts *Options) {
		opts.Revocation = &RevocationList{IDs: []string{"L-3"}}
	})
	if bound.Status() != StatusRevoked || bound.Valid() {
		t.Fatalf("status = %s", bound.Status())
	}
}

func TestManagerClockRollback(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	options := IssueOptions{ID: "L-clock", Product: "goframe", IssuedAt: now}
	publicKey, privateKey := testKeyPair(t)
	content, _, err := Issue(options, privateKey)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	stateFile := filepath.Join(t.TempDir(), "state.json")
	newManager := func(clock time.Time) *Manager {
		manager, err := New(Options{
			PublicKey:      publicKey,
			LicenseData:    content,
			StateFile:      stateFile,
			Now:            func() time.Time { return clock },
			ClockTolerance: 5 * time.Minute,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return manager
	}

	if got := newManager(now).Status(); got != StatusValid {
		t.Fatalf("first status = %s", got)
	}
	if got := newManager(now.Add(-time.Hour)).Status(); got != StatusClockRollback {
		t.Fatalf("rollback status = %s", got)
	}
	if got := newManager(now.Add(time.Minute)).Status(); got != StatusValid {
		t.Fatalf("forward status = %s", got)
	}
}

func TestManagerReload(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	publicKey, privateKey := testKeyPair(t)
	path := filepath.Join(t.TempDir(), "license.json")

	write := func(feature string) {
		content, _, err := Issue(IssueOptions{
			ID:       "L-reload",
			Product:  "goframe",
			IssuedAt: now,
			Features: []string{feature},
		}, privateKey)
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("write license: %v", err)
		}
	}

	write("sso")
	manager, err := New(Options{
		PublicKey:   publicKey,
		LicenseFile: path,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !manager.HasFeature("sso") {
		t.Fatal("sso feature missing")
	}

	write("audit")
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !manager.HasFeature("audit") || manager.HasFeature("sso") {
		t.Fatal("reload did not replace license")
	}
}

func TestNewRejectsInvalidLicense(t *testing.T) {
	publicKey, privateKey := testKeyPair(t)
	now := time.Now()
	content, _, err := Issue(IssueOptions{ID: "L-4", Product: "goframe", IssuedAt: now}, privateKey)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	tampered := append([]byte(nil), content...)
	tampered[len(tampered)-5] ^= 0xff

	if _, err := New(Options{PublicKey: publicKey, LicenseData: tampered}); err == nil {
		t.Fatal("expected invalid license error")
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected missing public key error")
	}
}
