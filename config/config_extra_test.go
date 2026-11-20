package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestByteSizeString(t *testing.T) {
	cases := map[ByteSize]string{
		0:          "0B",
		1023:       "1023B",
		1024:       "1.0KiB",
		1 << 20:    "1.0MiB",
		1536 << 10: "1.5MiB",
		1 << 30:    "1.0GiB",
		1 << 40:    "1.0TiB",
		1 << 50:    "1.0PiB",
		1 << 60:    "1.0EiB",
	}
	for size, want := range cases {
		if got := size.String(); got != want {
			t.Fatalf("ByteSize(%d).String() = %q, want %q", int64(size), got, want)
		}
		if size.Bytes() != int64(size) {
			t.Fatalf("Bytes() = %d, want %d", size.Bytes(), int64(size))
		}
	}
}

func TestParseByteSizeUnitsAndErrors(t *testing.T) {
	cases := map[string]int64{
		"1TB":    1 << 40,
		"1TIB":   1 << 40,
		"1GB":    1 << 30,
		"1B":     1,
		" 1 kb ": 1024,
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

	if _, err := ParseByteSize(""); err == nil {
		t.Fatal("expected error for empty input")
	}
	if _, err := ParseByteSize("   "); err == nil {
		t.Fatal("expected error for blank input")
	}
	if _, err := ParseByteSize("-2"); err == nil {
		t.Fatal("expected error for negative value")
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("CFG_EXPAND_SET", "value")
	t.Setenv("CFG_EXPAND_EMPTY", "")

	if got := expandEnv("a=${CFG_EXPAND_SET}"); got != "a=value" {
		t.Fatalf("expand = %q", got)
	}
	if got := expandEnv("b=${CFG_EXPAND_MISSING:-fallback}"); got != "b=fallback" {
		t.Fatalf("default expand = %q", got)
	}
	if got := expandEnv("c=${CFG_EXPAND_EMPTY:-dflt}"); got != "c=dflt" {
		t.Fatalf("empty env expand = %q", got)
	}
	if got := expandEnv("d=${CFG_EXPAND_EMPTY}"); got != "d=" {
		t.Fatalf("empty env without default = %q", got)
	}
	if got := expandEnv("plain"); got != "plain" {
		t.Fatalf("plain = %q", got)
	}
}

func TestRegisterRejectsEmptyName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for empty section name")
		}
	}()
	Register("   ", struct{}{})
}

type collectInner struct {
	B string `yaml:"b"`
}

type collectFixture struct {
	A       string        `yaml:"a"`
	Ptr     *collectInner `yaml:"ptr"`
	Skipped string        `yaml:"-"`
	List    []string      `yaml:"list"`
	hidden  string
}

func TestRegisterCollectsKeys(t *testing.T) {
	section := Register("test.collect", collectFixture{})
	if section.Name() != "test.collect" {
		t.Fatalf("name = %q", section.Name())
	}
	keys := section.envKeys()
	want := []string{"test.collect.a", "test.collect.ptr.b", "test.collect.list"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v, want %v", keys, want)
	}

	scalar := Register("test.scalar", 5)
	if scalar.Defaults() != 5 {
		t.Fatalf("defaults = %d", scalar.Defaults())
	}
	if got := scalar.envKeys(); len(got) != 1 || got[0] != "test.scalar" {
		t.Fatalf("scalar keys = %v", got)
	}
}

func TestEnvName(t *testing.T) {
	if got := envName("test.max-size"); got != "GOFRAME_TEST_MAX_SIZE" {
		t.Fatalf("envName = %q", got)
	}
}

func TestFileAccessors(t *testing.T) {
	SetFile("custom.yaml")
	defer SetFile("")
	if got := File(); got != "custom.yaml" {
		t.Fatalf("File = %q", got)
	}
}

func TestOnReloadCallbacksAndPanicIsolation(t *testing.T) {
	var calls int
	var once sync.Once
	OnReload(func() { calls++ })
	OnReload(func() {
		once.Do(func() { panic("callback boom") })
	})
	OnReload(nil)

	SetFile("")
	t.Setenv("GOFRAME_CONFIG", "")
	if err := Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if err := Reload(); err != nil {
		t.Fatalf("second Reload: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

type watchConfig struct {
	Name string `yaml:"name"`
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func waitForName(t *testing.T, section *Section[watchConfig], want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if section.Get().Name == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("name = %q, want %q", section.Get().Name, want)
}

func TestStartupWatchesFileChanges(t *testing.T) {
	section := Register("test.watch", watchConfig{Name: "default"})
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, "test:\n  watch:\n    name: one\n")

	SetFile(path)
	defer SetFile("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := Startup(ctx); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	defer func() { _ = Shutdown(ctx) }()
	// 等待文件监听协程完成 watcher 注册。
	time.Sleep(300 * time.Millisecond)

	if got := section.Get().Name; got != "one" {
		t.Fatalf("initial name = %q", got)
	}

	// 无关文件的写入事件应被过滤。
	writeFile(t, filepath.Join(dir, "other.txt"), "noise")

	writeFile(t, path, "test:\n  watch:\n    name: two\n")
	waitForName(t, section, "two")

	writeFile(t, path, "test:\n  watch:\n    name: three\n")
	waitForName(t, section, "three")

	// 非法配置触发热重载失败，旧值保持不变。
	writeFile(t, path, "test: [unclosed\n")
	time.Sleep(500 * time.Millisecond)
	if got := section.Get().Name; got != "three" {
		t.Fatalf("name after invalid reload = %q", got)
	}

	// 删除文件产生的非写入事件应被忽略。
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	if err := Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestStartupWithoutFile(t *testing.T) {
	SetFile("")
	t.Setenv("GOFRAME_CONFIG", "")
	if err := Startup(nil); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestStartupExplicitFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	SetFile(path)
	defer SetFile("")

	if err := Startup(context.Background()); err == nil {
		_ = Shutdown(context.Background())
		t.Fatal("expected error for missing explicit file")
	}
	if err := Reload(); err == nil {
		t.Fatal("expected Reload error for missing explicit file")
	}
}

func TestStartupFromEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	SetFile("")
	t.Setenv("GOFRAME_CONFIG", path)

	if err := Startup(context.Background()); err == nil {
		_ = Shutdown(context.Background())
		t.Fatal("expected error for missing GOFRAME_CONFIG file")
	}
}

func TestReloadParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	writeFile(t, path, "test: [unclosed\n")
	SetFile(path)
	defer SetFile("")

	if err := Reload(); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadSectionsMissingNonExplicit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")
	if err := loadSections(path, false); err != nil {
		t.Fatalf("loadSections: %v", err)
	}
}

type nestedConfig struct {
	Deep struct {
		Value string `yaml:"value"`
	} `yaml:"deep"`
}

func TestEnvOverrideNested(t *testing.T) {
	section := Register("test.nested", nestedConfig{})
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "test:\n  nested:\n    deep:\n      value: from-file\n")
	SetFile(path)
	defer SetFile("")
	t.Setenv("GOFRAME_TEST_NESTED_DEEP_VALUE", "from-env")

	if err := Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := section.Get().Deep.Value; got != "from-env" {
		t.Fatalf("value = %q, want from-env", got)
	}
}

func TestEnvOverrideReplacesScalarParent(t *testing.T) {
	section := Register("test.nested.scalar", nestedConfig{})
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "test: scalar\n")
	SetFile(path)
	defer SetFile("")
	t.Setenv("GOFRAME_TEST_NESTED_SCALAR_DEEP_VALUE", "from-env")

	if err := Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := section.Get().Deep.Value; got != "from-env" {
		t.Fatalf("value = %q, want from-env", got)
	}
}

type decodeConfig struct {
	Size ByteSize `yaml:"size"`
}

func TestReloadDecodeError(t *testing.T) {
	Register("test.decode", decodeConfig{})
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "test:\n  decode:\n    size: banana\n")
	SetFile(path)
	defer SetFile("")

	if err := Reload(); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestConcurrentReadDuringReload(t *testing.T) {
	section := Register("test.concurrent", watchConfig{Name: "default"})
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "test:\n  concurrent:\n    name: initial\n")
	SetFile(path)
	defer SetFile("")

	if err := Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if section.Get() == nil {
						t.Error("nil snapshot")
						return
					}
				}
			}
		}()
	}

	for i := 0; i < 20; i++ {
		writeFile(t, path, fmt.Sprintf("test:\n  concurrent:\n    name: v%d\n", i))
		if err := Reload(); err != nil {
			t.Fatalf("Reload: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}

func TestWatchSectionsAddError(t *testing.T) {
	// 目录不存在时 watcher.Add 失败，函数应立即返回。
	watchSections(context.Background(), filepath.Join(t.TempDir(), "missing", "config.yaml"))
}

func TestStartupResolvesDefaultFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), "app:\n  name: from-default\n")
	t.Chdir(dir)
	SetFile("")
	defer SetFile("")
	t.Setenv("GOFRAME_CONFIG", "")

	ctx := context.Background()
	if err := Startup(ctx); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	defer func() { _ = Shutdown(ctx) }()

	if got := App().Name; got != "from-default" {
		t.Fatalf("app name = %q, want from-default", got)
	}
}

type collectDefault struct {
	Untagged string
	Skipped  string `yaml:"-"`
	hidden   string
}

type collectEmpty struct {
	Skipped string `yaml:"-"`
	hidden  string
}

func TestRegisterCollectsDefaultAndEmptyKeys(t *testing.T) {
	section := Register("test.collectdefault", collectDefault{})
	keys := section.envKeys()
	if len(keys) != 1 || keys[0] != "test.collectdefault.untagged" {
		t.Fatalf("keys = %v", keys)
	}

	empty := Register("test.collectempty", collectEmpty{})
	keys = empty.envKeys()
	if len(keys) != 1 || keys[0] != "test.collectempty" {
		t.Fatalf("empty keys = %v", keys)
	}
}

type lookupConfig struct {
	V string `yaml:"v"`
}

func TestLookupSkipsNonMapParent(t *testing.T) {
	section := Register("test.lookup.deep", lookupConfig{V: "default"})
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "test: scalar\n")
	SetFile(path)
	defer SetFile("")

	if err := Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := section.Get().V; got != "default" {
		t.Fatalf("value = %q, want default", got)
	}
}
