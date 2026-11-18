package i18n

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestBundle() *Bundle {
	bundle := New("zh-CN")
	bundle.Add("zh-CN", map[string]string{
		"hello": "你好，%s",
		"only":  "仅中文",
	})
	bundle.Add("zh-Hant", map[string]string{
		"hello": "妳好，%s",
	})
	bundle.Add("en", map[string]string{
		"hello": "Hello, %s",
	})
	return bundle
}

func TestTranslateFormatAndFallback(t *testing.T) {
	bundle := newTestBundle()

	cases := []struct {
		name   string
		locale string
		key    string
		args   []any
		want   string
	}{
		{name: "exact", locale: "zh-CN", key: "hello", args: []any{"张三"}, want: "你好，张三"},
		{name: "case insensitive", locale: "ZH-cn", key: "hello", args: []any{"张三"}, want: "你好，张三"},
		{name: "parent locale", locale: "zh-Hant-TW", key: "hello", args: []any{"张三"}, want: "妳好，张三"},
		{name: "default locale", locale: "fr-FR", key: "hello", args: []any{"张三"}, want: "你好，张三"},
		{name: "language variant", locale: "zh", key: "only", want: "仅中文"},
		{name: "missing key", locale: "en", key: "missing", want: "missing"},
		{name: "empty key", locale: "en", key: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bundle.Translate(tc.locale, tc.key, tc.args...); got != tc.want {
				t.Fatalf("Translate(%q, %q) = %q, want %q", tc.locale, tc.key, got, tc.want)
			}
		})
	}
}

func TestAddMergesMessages(t *testing.T) {
	bundle := New("en")
	bundle.Add("en", map[string]string{"a": "A"})
	bundle.Add("en", map[string]string{"b": "B", "a": "A2"})

	if got := bundle.Translate("en", "a"); got != "A2" {
		t.Fatalf("Translate a = %q, want A2", got)
	}
	if got := bundle.Translate("en", "b"); got != "B" {
		t.Fatalf("Translate b = %q, want B", got)
	}
	if got := bundle.Locales(); !reflect.DeepEqual(got, []string{"en"}) {
		t.Fatalf("Locales = %v, want [en]", got)
	}
}

func TestContextLocale(t *testing.T) {
	if got := Locale(context.Background()); got != "" {
		t.Fatalf("Locale(background) = %q, want empty", got)
	}

	ctx := WithLocale(context.Background(), "ZH_TW")
	if got := Locale(ctx); got != "zh-tw" {
		t.Fatalf("Locale(ctx) = %q, want zh-tw", got)
	}
}

func TestParseAcceptLanguage(t *testing.T) {
	cases := []struct {
		header string
		want   []string
	}{
		{header: "en-US,en;q=0.9,zh-CN;q=0.8", want: []string{"en-US", "en", "zh-CN"}},
		{header: "fr;q=0.3, zh-CN ; q=0.9, *;q=0.5", want: []string{"zh-CN", "fr"}},
		{header: "de;q=0, en;q=0.5", want: []string{"en"}},
		{header: "", want: []string{}},
		{header: "ja, ko;q=0.5, zh;q=0.5", want: []string{"ja", "ko", "zh"}},
	}
	for _, tc := range cases {
		if got := ParseAcceptLanguage(tc.header); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("ParseAcceptLanguage(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestMiddlewareDetectionOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bundle := newTestBundle()

	engine := gin.New()
	engine.Use(bundle.Middleware())
	engine.GET("/locale", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"gin":     c.GetString(LocaleKey),
			"context": Locale(c.Request.Context()),
			"helper":  LocaleFromGin(c),
		})
	})

	cases := []struct {
		name    string
		target  string
		headers map[string]string
		cookie  string
		want    string
	}{
		{
			name:    "header wins",
			target:  "/locale?lang=en",
			headers: map[string]string{"X-Locale": "zh-Hant-TW", "Accept-Language": "en"},
			cookie:  "en",
			want:    "zh-hant",
		},
		{
			name:   "query before cookie",
			target: "/locale?lang=en",
			cookie: "zh-CN",
			want:   "en",
		},
		{
			name:    "accept language q order",
			target:  "/locale",
			headers: map[string]string{"Accept-Language": "fr-FR,en;q=0.9,zh-CN;q=0.5"},
			want:    "en",
		},
		{
			name:   "cookie fallback",
			target: "/locale",
			cookie: "zh_CN",
			want:   "zh-cn",
		},
		{
			name:   "default locale",
			target: "/locale",
			want:   "zh-cn",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			for key, value := range tc.headers {
				req.Header.Set(key, value)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "locale", Value: tc.cookie})
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)

			var body struct {
				Gin     string `json:"gin"`
				Context string `json:"context"`
				Helper  string `json:"helper"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Gin != tc.want || body.Context != tc.want || body.Helper != tc.want {
				t.Fatalf("locale = %+v, want %q", body, tc.want)
			}
		})
	}
}

func TestBundleConcurrentAccess(t *testing.T) {
	bundle := New("en")
	bundle.Add("en", map[string]string{"hello": "Hello"})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = bundle.Translate("en", "hello")
			}
		}()
		go func(i int) {
			defer wg.Done()
			bundle.Add("zh-CN", map[string]string{"hello": "你好"})
		}(i)
	}
	wg.Wait()
}
