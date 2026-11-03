package clickup

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPageLimitRejectsPartialCount(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, `{"tasks":[{"id":"video","name":"Video","parent":"parent","status":{"type":"closed","status":"complete"}}],"last_page":false}`)
	}))
	defer srv.Close()
	c := NewHTTPClient(srv.URL, "test", "42", quiet)
	c.MaxPages = 1
	for i := 0; i < 2; i++ {
		if tasks, err := c.Tasks(context.Background(), TaskFilter{IncludeClosed: true}); err == nil || tasks != nil {
			t.Fatal("partial financial count returned")
		}
	}
	if requests != 2 {
		t.Fatal("partial task list was cached")
	}
}
