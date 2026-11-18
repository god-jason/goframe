package i18n

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNewDefaultLocaleAndNormalization(t *testing.T) {
	if got := New("").DefaultLocale(); got != "en" {
		t.Fatalf("DefaultLocale = %q, want en", got)
	}
	if got := New("  ZH_TW ").DefaultLocale(); got != "zh-tw" {
		t.Fatalf("DefaultLocale = %q, want zh-tw", got)
	}
}

func TestAddIgnoresInvalidInput(t *testing.T) {
	bundle := New("en")
	bundle.Add("", map[string]string{"a": "A"})
	bundle.Add("en", nil)
	bundle.Add("en", map[string]string{})

	if got := bundle.Locales(); len(got) != 0 {
		t.Fatalf("Locales = %v, want empty", got)
	}

	bundle.Add("en", map[string]string{"": "empty", "   ": "blank", " ok ": "value"})
	if got := bundle.Translate("en", "ok"); got != "value" {
		t.Fatalf("Translate ok = %q, want value", got)
	}
	if got := bundle.Locales(); !reflect.DeepEqual(got, []string{"en"}) {
		t.Fatalf("Locales = %v, want [en]", got)
	}
}

func TestMatch(t *testing.T) {
	bundle := newTestBundle()

	cases := []struct {
		locale string
		want   string
		ok     bool
	}{
		{locale: "", ok: false},
		{locale: "zh-CN", want: "zh-cn", ok: true},
		{locale: "zh-Hant-TW", want: "zh-hant", ok: true},
		{locale: "zh", want: "zh-cn", ok: true},
		{locale: "EN", want: "en", ok: true},
		{locale: "zh-TW", ok: false},
		{locale: "fr-FR", ok: false},
	}
	for _, tc := range cases {
		got, ok := bundle.Match(tc.locale)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("Match(%q) = %q, %v; want %q, %v", tc.locale, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTranslateFallbackChain(t *testing.T) {
	bundle := New("fr")
	bundle.Add("fr", map[string]string{"hello": "Bonjour, %s"})
	bundle.Add("zh-Hant", map[string]string{"hello": "妳好，%s"})

	if got := bundle.Translate("zh-Hant-TW", "hello", "A"); got != "妳好，A" {
		t.Fatalf("Translate = %q", got)
	}
	if got := bundle.Translate("zh-CN", "hello", "A"); got != "Bonjour, A" {
		t.Fatalf("Translate default = %q", got)
	}
	if got := bundle.Translate("zh-Hant-TW", "missing"); got != "missing" {
		t.Fatalf("Translate missing = %q", got)
	}
}

func TestLocaleFromGinFallbacks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	// 请求为 nil 时返回空字符串。
	if got := LocaleFromGin(c); got != "" {
		t.Fatalf("LocaleFromGin(nil request) = %q", got)
	}

	// Context 中存在非字符串值时回退到请求 context。
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(WithLocale(request.Context(), "ZH_CN"))
	c, _ = gin.CreateTestContext(recorder)
	c.Request = request
	c.Set(LocaleKey, 42)
	if got := LocaleFromGin(c); got != "zh-cn" {
		t.Fatalf("LocaleFromGin = %q, want zh-cn", got)
	}

	// 请求 context 也没有语言时返回空。
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if got := LocaleFromGin(c); got != "" {
		t.Fatalf("LocaleFromGin = %q, want empty", got)
	}
}

func TestMiddlewareWithCustomConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bundle := newTestBundle()

	engine := gin.New()
	engine.Use(bundle.MiddlewareWithConfig(MiddlewareConfig{HeaderName: "X-Lang"}))
	engine.GET("/locale", func(c *gin.Context) {
		c.String(http.StatusOK, LocaleFromGin(c))
	})

	// 仅覆盖 HeaderName，QueryName/CookieName 应回退到默认值。
	req := httptest.NewRequest(http.MethodGet, "/locale?lang=en", nil)
	req.Header.Set("X-Lang", "zh-CN")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	if got := recorder.Body.String(); got != "zh-cn" {
		t.Fatalf("locale = %q, want zh-cn", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/locale?lang=en", nil)
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	if got := recorder.Body.String(); got != "en" {
		t.Fatalf("locale = %q, want en", got)
	}

	// 完全自定义名称。
	if cfg := (MiddlewareConfig{CookieName: "custom"}).withDefaults(); cfg.HeaderName != DefaultHeaderName || cfg.QueryName != DefaultQueryName || cfg.CookieName != "custom" {
		t.Fatalf("withDefaults = %+v", cfg)
	}
}

func TestParseAcceptLanguageEdgeCases(t *testing.T) {
	cases := []struct {
		header string
		want   []string
	}{
		{header: "en;q=2", want: []string{"en"}},
		{header: "en;q=bogus,fr", want: []string{"en", "fr"}},
		{header: "en;q,fr;q=0.5", want: []string{"en", "fr"}},
		{header: " ;q=0.5, *;q=0.9, de", want: []string{"de"}},
		{header: "a;q=0.5,b;q=0.5,c;q=0.9", want: []string{"c", "a", "b"}},
		{header: "x;q=, y;q=-1", want: []string{"x"}},
	}
	for _, tc := range cases {
		if got := ParseAcceptLanguage(tc.header); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("ParseAcceptLanguage(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestBundleConcurrentMatchAndAdd(t *testing.T) {
	bundle := newTestBundle()
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				_, _ = bundle.Match("zh-Hant-TW")
				_ = bundle.Locales()
				bundle.Add("en", map[string]string{"k": "v"})
				_ = bundle.Translate("zh-CN", "hello", "x")
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestLocaleContextKeyIsolation(t *testing.T) {
	ctx := WithLocale(context.Background(), "en")
	if Locale(ctx) != "en" {
		t.Fatalf("Locale = %q", Locale(ctx))
	}
	other := context.WithValue(ctx, struct{ x int }{}, "ignored")
	if Locale(other) != "en" {
		t.Fatalf("Locale after unrelated value = %q", Locale(other))
	}
}
