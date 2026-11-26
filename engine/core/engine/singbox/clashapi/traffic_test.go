package clashapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTotalsIncludesClosedConnections(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/connections" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		fmt.Fprint(w, `{"uploadTotal":1500,"downloadTotal":9000,"connections":[]}`)
	}))
	defer s.Close()
	up, down, err := New(s.URL).Totals()
	if err != nil || up != 1500 || down != 9000 {
		t.Fatalf("lost completed traffic: %d %d %v", up, down, err)
	}
}

func TestTotalsHasShortDeadlineWhenCoreStalls(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer s.Close()
	start := time.Now()
	if _, _, err := New(s.URL).Totals(); err == nil {
		t.Fatal("stalled core returned valid counters")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("statistics blocked behind the normal core timeout")
	}
}
