package sibrowser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIReportsModerationBlockSeparatelyFromCredentialDenial(t *testing.T) {
	blocked := true
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		if blocked {
			w.Write([]byte(`{"error":"blocked"}`))
		} else {
			w.Write([]byte(`{"error":"forbidden"}`))
		}
	}))
	defer h.Close()
	c, e := newAPI(h.URL, "token", false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.publish(context.Background(), "1")
	var f *Fault
	if !errors.As(e, &f) || f.Code != "publication_suppressed" {
		t.Fatal(e)
	}
	blocked = false
	_, e = c.publish(context.Background(), "1")
	if !errors.As(e, &f) || f.Code != "api_http_403" {
		t.Fatal(e)
	}
}
