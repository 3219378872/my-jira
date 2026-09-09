package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"my-jira/apps/api/internal/platform/objectstore"
	"my-jira/apps/api/internal/support/testutil"
)

func TestSnapshotValidationRejectsChangedContentAndEscapingFiles(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "objects"), 0700); err != nil {
		t.Fatal(err)
	}
	key, content := "assets/test/example.txt", "Saved content"
	keyHash, checksum := sha256.Sum256([]byte(key)), sha256.Sum256([]byte(content))
	item := entry{Key: key, File: hex.EncodeToString(keyHash[:]), Size: int64(len(content)), SHA256: hex.EncodeToString(checksum[:]), ContentType: "text/plain"}
	snapshot := manifest{Version: 2, Bucket: "original-test", Objects: []entry{item}}
	save := func(value manifest) {
		t.Helper()
		body, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(directory, "objects.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(directory, "objects", item.File)
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	save(snapshot)
	if _, err := verifySnapshot(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("Changed content"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifySnapshot(directory); err == nil {
		t.Fatal("changed snapshot bytes passed verification")
	}
	outside := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(outside, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Fatal(err)
	}
	if _, err := verifySnapshot(directory); err == nil {
		t.Fatal("an object symlink escaped the snapshot root")
	}
	snapshot.Objects[0].File = "../../external.txt"
	save(snapshot)
	if _, err := verifySnapshot(directory); err == nil {
		t.Fatal("an escaping manifest path was accepted")
	}
}

func TestS3RestorePreservesObjectsAndRefusesExistingTargets(t *testing.T) {
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_TEST_ENDPOINT is required for explicit isolated-bucket integration")
	}
	f := testutil.New(t)
	var schema string
	if err := f.DB.SQL.QueryRow("SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	t.Setenv("DATABASE_URL", dsn.String())
	t.Setenv("S3_ENDPOINT", endpoint)
	source := "myjira-backup-test-" + uuid.NewString()
	t.Setenv("S3_BUCKET", source)
	ctx := context.Background()
	store, _, err := objectstore.Load(ctx, f.DB.SQL)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MakeBucket(ctx, source, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	const key = "assets/test/content.txt"
	t.Cleanup(func() {
		if err := store.RemoveObject(context.Background(), source, key, minio.RemoveObjectOptions{}); err != nil {
			t.Error(err)
		}
		if err := store.RemoveBucket(context.Background(), source); err != nil {
			t.Error(err)
		}
	})
	const content = "Round trip object data"
	options := minio.PutObjectOptions{ContentType: "text/plain; charset=utf-8", CacheControl: "private, max-age=17", ContentDisposition: "attachment", ContentLanguage: "en", UserMetadata: map[string]string{"fixture": "original metadata"}}
	if _, err := store.PutObject(ctx, source, key, strings.NewReader(content), int64(len(content)), options); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := backupSnapshot(directory); err != nil {
		t.Fatal(err)
	}
	snapshot, err := verifySnapshot(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Objects) != 1 || !sameMetadata(snapshot.Objects[0].Metadata, options.UserMetadata) {
		t.Fatalf("snapshot metadata: %#v", snapshot)
	}
	if err := restoreSnapshot(directory, snapshot, source, false, true); err == nil {
		t.Fatal("restore accepted its original bucket")
	}
	if err := verifyRestoredObject(ctx, store, source, snapshot.Objects[0]); err != nil {
		t.Fatal("source object changed: ", err)
	}
	target := "myjira-restore-test-" + uuid.NewString()
	if err := restoreSnapshot(directory, snapshot, target, false, true); err != nil {
		t.Fatal(err)
	}
	if exists, err := store.BucketExists(ctx, target); err != nil || exists {
		t.Fatalf("owned restore target was not removed: exists=%v error=%v", exists, err)
	}
	existing := "myjira-restore-test-" + uuid.NewString()
	if err := store.MakeBucket(ctx, existing, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.RemoveBucket(context.Background(), existing) })
	if err := restoreSnapshot(directory, snapshot, existing, false, true); err == nil {
		t.Fatal("existing bucket accepted for overwrite")
	}
	if exists, err := store.BucketExists(ctx, existing); err != nil || !exists {
		t.Fatal("preexisting bucket was removed during rejection")
	}
	if err := restoreSnapshot(directory, snapshot, target, true, true); err == nil {
		t.Fatal("application verifier accepted a database outside the explicit restore namespace")
	}
	if exists, err := store.BucketExists(ctx, target); err != nil || exists {
		t.Fatal("database guard mutated object storage before rejecting")
	}
}
