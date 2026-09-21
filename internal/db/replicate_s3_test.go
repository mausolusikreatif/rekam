package db

import (
	"context"
	"testing"
)

// Scenarios: spec/replication.md (REPL-13)

// TestNewS3ReplicationRequiresBucket covers the one S3-wiring assertion
// testable with no network access: a missing Bucket is rejected before any
// AWS client is built. See spec/replication.md's GAPs section — the rest of
// replicate_s3.go (prefix/region defaulting, key layout, endpoint/path-style
// handling for R2/MinIO, Owner propagation into the leaser) has no coverage
// yet and needs a real or fake S3 endpoint to test properly.
func TestNewS3ReplicationRequiresBucket(t *testing.T) {
	// REPL-13
	if _, err := NewS3Replication(context.Background(), S3Config{}, ReplicationOptions{}); err == nil {
		t.Fatal("expected an error for a missing Bucket, got nil")
	}
}
