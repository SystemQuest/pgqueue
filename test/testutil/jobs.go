package testutil

import (
	"fmt"
	"testing"
	"time"
)

// CreateTestJobs creates test jobs for testing - similar to pgqueuer's job creation patterns
func CreateTestJobs(n int) []TestJobRequest {
	jobs := make([]TestJobRequest, n)
	for i := 0; i < n; i++ {
		jobs[i] = TestJobRequest{
			Entrypoint: "test.job",
			Payload:    []byte(fmt.Sprintf(`{"id": %d, "data": "test_%d"}`, i, i)),
			Priority:   int32(i % 10), // Vary priority like pgqueuer tests
		}
	}
	return jobs
}

// TestJobRequest represents a job creation request for testing
type TestJobRequest struct {
	Entrypoint string
	Payload    []byte
	Priority   int32
}

// WaitForCondition waits for a condition to be true with timeout
// Similar to pgqueuer's asyncio.wait_for patterns
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, message string) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if condition() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("Timeout waiting for condition: %s", message)
			}
		}
	}
}

// ExtractPayloadIDs extracts ID values from job payloads for comparison
// Helper for test assertions similar to pgqueuer's payload handling
func ExtractPayloadIDs(payloads [][]byte) []int {
	var ids []int
	for _, payload := range payloads {
		if payload != nil {
			// Simple extraction assuming format {"id": N, ...}
			var id int
			fmt.Sscanf(string(payload), `{"id": %d`, &id)
			ids = append(ids, id)
		}
	}
	return ids
}
