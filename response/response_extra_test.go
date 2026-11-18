package response

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	apperrors "github.com/god-jason/goframe/errors"
)

func TestCreatedAndAccepted(t *testing.T) {
	e := newTestEngine()
	e.POST("/created", func(c *gin.Context) { Created(c, gin.H{"id": 1}) })
	e.POST("/accepted", func(c *gin.Context) { Accepted(c, gin.H{"job": "queued"}) })

	created := perform(e, http.MethodPost, "/created")
	if created.Code != http.StatusCreated {
		t.Fatalf("created status = %d", created.Code)
	}
	if body := decodeBody[Response[map[string]any]](t, created); body.Message != "created" || body.Data["id"] != float64(1) {
		t.Fatalf("unexpected created envelope: %+v", body)
	}

	accepted := perform(e, http.MethodPost, "/accepted")
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("accepted status = %d", accepted.Code)
	}
	if body := decodeBody[Response[map[string]any]](t, accepted); body.Message != "accepted" || body.Code != apperrors.CodeOK {
		t.Fatalf("unexpected accepted envelope: %+v", body)
	}
}

func TestFailNilErrorBecomesInternal(t *testing.T) {
	e := newTestEngine()
	e.GET("/nil", func(c *gin.Context) { Fail(c, nil) })

	recorder := perform(e, http.MethodGet, "/nil")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := decodeBody[Response[any]](t, recorder)
	if body.Code != apperrors.CodeInternal {
		t.Fatalf("code = %d", body.Code)
	}
}

func TestFailZeroStatusFallsBackTo500(t *testing.T) {
	e := newTestEngine()
	e.GET("/zero", func(c *gin.Context) {
		Fail(c, apperrors.New(12345, 0, "zero status"))
	})

	recorder := perform(e, http.MethodGet, "/zero")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := decodeBody[Response[any]](t, recorder)
	if body.Code != 12345 || body.Message != "zero status" {
		t.Fatalf("unexpected envelope: %+v", body)
	}
}

func TestFailWithNoContentWritesEmptyBody(t *testing.T) {
	e := newTestEngine()
	e.DELETE("/none", func(c *gin.Context) {
		FailWith(c, http.StatusNoContent, 0, "")
	})

	recorder := perform(e, http.MethodDelete, "/none")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestSortClauseFallbackAndInvalidDirection(t *testing.T) {
	query := PageQuery{Sort: "unknown", Order: "SIDEWAYS"}
	column, direction := query.SortClause(map[string]string{}, "")
	if column != "" || direction != "desc" {
		t.Fatalf("empty whitelist = (%q, %q), want (\"\", desc)", column, direction)
	}

	query.Order = "asc"
	column, direction = query.SortClause(map[string]string{"known": "known_col"}, "known")
	if column != "known_col" || direction != "asc" {
		t.Fatalf("fallback = (%q, %q)", column, direction)
	}

	column, direction = query.SortClause(nil, "raw_fallback")
	if column != "raw_fallback" || direction != "asc" {
		t.Fatalf("raw fallback = (%q, %q)", column, direction)
	}
}
