package payment

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
)

func TestRegisterNotifyRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api.Reset()
	defer api.Reset()

	register(Config{Enabled: true, NotifyPath: "/payment/notify"})
	paths := map[string]bool{}
	for _, entry := range api.Entries() {
		if entry.Method == http.MethodPost {
			paths[entry.Method+" "+entry.Path] = true
		}
	}
	for _, want := range []string{"POST /payment/notify/wechat", "POST /payment/notify/alipay"} {
		if !paths[want] {
			t.Fatalf("missing route %q, entries = %v", want, paths)
		}
	}

	api.Reset()
	register(Config{Enabled: false})
	if len(api.Entries()) != 0 {
		t.Fatal("disabled config must not register routes")
	}

	api.Reset()
	register(Config{Enabled: true, NotifyPath: ""})
	found := false
	for _, entry := range api.Entries() {
		if entry.Path == "/payment/notify/wechat" {
			found = true
		}
	}
	if !found {
		t.Fatal("empty notify path must fall back to default")
	}
}
