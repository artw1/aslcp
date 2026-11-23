package api

import (
	"context"
	"net/http/httptest"
	"testing"
)

// A multi-step apply must not stop when the browser goes away.
func TestApplyContextSurvivesCancel(t *testing.T) {
	rc, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/x", nil).WithContext(rc)
	ctx, done := applyContext(r)
	defer done()
	cancel()
	if err := ctx.Err(); err != nil {
		t.Fatalf("apply context ended with the request: %v", err)
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("apply context has no deadline")
	}
}
