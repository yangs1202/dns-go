package dns

import "testing"

func TestHandlerStatsIntegration(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	if handler.stats == nil {
		t.Fatalf("expected stats to be set")
	}
}
