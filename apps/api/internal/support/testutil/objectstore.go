package testutil

import (
	"os"
	"testing"

	"github.com/google/uuid"
)

// ObjectStoreEnvironment selects an explicitly configured test store and a new
// bucket. Callers own cleanup of that bucket and its objects.
func ObjectStoreEnvironment(t *testing.T) {
	t.Helper()
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_S3_ENDPOINT is required for isolated object-store integration")
	}
	t.Setenv("S3_ENDPOINT", endpoint)
	t.Setenv("S3_BUCKET", "test-"+uuid.NewString())
	for name, fallback := range map[string]string{"S3_ACCESS_KEY": "myjira_local", "S3_SECRET_KEY": "myjira_local_storage", "S3_USE_SSL": "false"} {
		value := os.Getenv("TEST_" + name)
		if value == "" {
			value = fallback
		}
		t.Setenv(name, value)
	}
}
