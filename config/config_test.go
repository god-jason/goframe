package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseByteSize(t *testing.T) {
	cases := map[string]int64{
		"1024":    1024,
		"1KB":     1024,
		"1KiB":    1024,
		"10MB":    10 * 1024 * 1024,
		"1.5GB":   1536 * 1024 * 1024,
		"512 MiB": 512 * 1024 * 1024,
	}
	for input, want := range cases {
		got, err := ParseByteSize(input)
		if err != nil {
			t.Fatalf("ParseByteSize(%q): %v", input, err)
		}
		if got.Bytes() != want {
			t.Fatalf("ParseByteSize(%q) = %d, want %d", input, got, want)
		}
	}

	if _, err := ParseByteSize("-1MB"); err == nil {
		t.Fatal("expected negative size to be rejected")
	}
	if _, err := ParseByteSize("abc"); err == nil {
		t.Fatal("expected invalid size to be rejected")
	}
}

type testSectionConfig struct {
	Name     string        `mapstructure:"name"`
	Timeout  time.Duration `mapstructure:"timeout"`
	Size     ByteSize      `mapstructure:"size"`
	MaxItems int           `mapstructure:"max_items"`
	Enabled  bool          `mapstructure:"enabled"`
}

func TestSectionLoadsFileAndKeepsDefaults(t *testing.T) {
	defaults := testSectionConfig{
		Name:     "default",
		Timeout:  3 * time.Second,
		Size:     1 << 20,
		MaxItems: 7,
		Enabled:  true,
	}
	section := Register("test.section", defaults)

	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "test:\n  section:\n    name: from-file\n    timeout: 5s\n    size: 2MB\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	SetFile(path)
	defer SetFile("")

	if err := Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	got := section.Get()
	if got.Name != "from-file" {
		t.Fatalf("name = %q", got.Name)
	}
	if got.Timeout != 5*time.Second {
		t.Fatalf("timeout = %v", got.Timeout)
	}
	if got.Size.Bytes() != 2<<20 {
		t.Fatalf("size = %d", got.Size.Bytes())
	}
	// 文件中缺失的字段保留默认值。
	if got.MaxItems != 7 || !got.Enabled {
		t.Fatalf("defaults not preserved: %+v", got)
	}
}

func TestSectionEnvOverride(t *testing.T) {
	section := Register("test.env", testSectionConfig{Name: "default", MaxItems: 1})
	t.Setenv("GOFRAME_TEST_ENV_NAME", "from-env")

	path := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(path, []byte("test:\n  env: {}\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	SetFile(path)
	defer SetFile("")
	if err := Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := section.Get().Name; got != "from-env" {
		t.Fatalf("name = %q, want from-env", got)
	}
}

func TestAppSectionDefault(t *testing.T) {
	app := App()
	if app.Name == "" || app.Version == "" || app.Environment == "" {
		t.Fatalf("unexpected app config: %+v", app)
	}
}
