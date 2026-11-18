package httpclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":7,"name":"demo"}`)
	}))
	defer server.Close()

	var out struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := New(Options{}).GetJSON(context.Background(), server.URL, &out); err != nil {
		t.Fatalf("get json: %v", err)
	}
	if out.ID != 7 || out.Name != "demo" {
		t.Fatalf("out = %+v", out)
	}
}

func TestPostJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		var in map[string]string
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"echo": in["name"]})
	}))
	defer server.Close()

	var out map[string]string
	err := New(Options{}).PostJSON(context.Background(), server.URL, map[string]string{"name": "demo"}, &out)
	if err != nil {
		t.Fatalf("post json: %v", err)
	}
	if out["echo"] != "demo" {
		t.Fatalf("out = %+v", out)
	}
}

func TestJSONAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":404,"message":"missing"}`)
	}))
	defer server.Close()

	err := New(Options{}).GetJSON(context.Background(), server.URL, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusNotFound {
		t.Fatalf("status = %d", apiErr.Status)
	}
	if !strings.Contains(apiErr.Body, "missing") {
		t.Fatalf("body = %q", apiErr.Body)
	}
	if !strings.HasPrefix(apiErr.Error(), "httpclient:") {
		t.Fatalf("error prefix missing: %v", apiErr)
	}
}
