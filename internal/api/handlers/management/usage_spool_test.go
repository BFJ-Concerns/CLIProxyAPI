package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagespool"
)

func serveUsageSpool(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	router := gin.New()
	router.GET("/usage-spool", h.GetUsageSpool)
	router.POST("/usage-spool/ack", h.AckUsageSpool)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestUsageSpoolEndpointsReadPageAndAck(t *testing.T) {
	spool, err := usagespool.Open(t.TempDir(), 14*24*time.Hour)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	previous := usagespool.SetDefault(spool)
	t.Cleanup(func() {
		usagespool.SetDefault(previous)
		_ = spool.Close()
	})
	for i := 0; i < 3; i++ {
		if _, err := spool.AppendUsage([]byte(`{"model":"m"}`), usagespool.Account{AuthIndex: "idx"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	rec := serveUsageSpool(t, http.MethodGet, "/usage-spool?kind=usage&after=0&limit=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	var page struct {
		Records       []map[string]any `json:"records"`
		NextAfter     int64            `json:"next_after"`
		OldestSeq     *int64           `json:"oldest_seq"`
		PrunedThrough *int64           `json:"pruned_through"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Records) != 2 || page.NextAfter != 2 || page.OldestSeq == nil || *page.OldestSeq != 1 || page.PrunedThrough != nil {
		t.Fatalf("page = %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"pruned_through":null`) {
		t.Fatalf("pruned_through not reported as null: %s", rec.Body)
	}

	rec = serveUsageSpool(t, http.MethodPost, "/usage-spool/ack", `{"kind":"usage","through":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ack status = %d body=%s", rec.Code, rec.Body)
	}
	rec = serveUsageSpool(t, http.MethodGet, "/usage-spool?kind=usage", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Records) != 1 || page.Records[0]["seq"].(float64) != 3 || *page.OldestSeq != 3 {
		t.Fatalf("after ack = %s", rec.Body)
	}
}

func TestUsageSpoolEndpointsRejectBadInput(t *testing.T) {
	previous := usagespool.SetDefault(nil)
	t.Cleanup(func() { usagespool.SetDefault(previous) })
	if rec := serveUsageSpool(t, http.MethodGet, "/usage-spool?kind=usage", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled status = %d", rec.Code)
	}

	spool, err := usagespool.Open(t.TempDir(), 14*24*time.Hour)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	usagespool.SetDefault(spool)
	t.Cleanup(func() { _ = spool.Close() })

	cases := []struct{ method, target, body string }{
		{http.MethodGet, "/usage-spool?kind=other", ""},
		{http.MethodGet, "/usage-spool?kind=usage&after=-1", ""},
		{http.MethodGet, "/usage-spool?kind=usage&limit=0", ""},
		{http.MethodPost, "/usage-spool/ack", `{"kind":"usage"}`},
		{http.MethodPost, "/usage-spool/ack", `{"kind":"other","through":1}`},
	}
	for _, tc := range cases {
		if rec := serveUsageSpool(t, tc.method, tc.target, tc.body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s %s: status = %d, want 400", tc.method, tc.target, tc.body, rec.Code)
		}
	}
}
