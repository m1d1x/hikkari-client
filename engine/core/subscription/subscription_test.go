package subscription

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The hub holds back what a released client's core would refuse, so a client
// that can dial AmneziaWG has to say so.
func TestFetchSaysWhatThisClientCanDial(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Hikkari-Features")
		fmt.Fprint(w, "x")
	}))
	defer srv.Close()
	_, _ = Fetch(context.Background(), srv.URL, "", func(s string) (string, error) { return s, nil })
	if got != "amneziawg" {
		t.Fatalf("Hikkari-Features = %q", got)
	}
}
