package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/objectstore"
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func verifySnapshot(directory string) (manifest, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return manifest{}, err
	}
	defer root.Close()
	file, err := root.Open("objects.json")
	if err != nil {
		return manifest{}, err
	}
	defer file.Close()
	var snapshot manifest
	decoder := json.NewDecoder(io.LimitReader(file, 64<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return snapshot, err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return snapshot, fmt.Errorf("manifest must contain one JSON object")
	}
	if snapshot.Version != 0 && snapshot.Version != 2 {
		return snapshot, fmt.Errorf("unsupported snapshot manifest version")
	}
	seen := map[string]bool{}
	for _, item := range snapshot.Objects {
		keyHash := sha256.Sum256([]byte(item.Key))
		if item.Key == "" || seen[item.Key] || !digestPattern.MatchString(item.File) || item.File != hex.EncodeToString(keyHash[:]) || !digestPattern.MatchString(item.SHA256) || item.Size < 0 {
			return snapshot, fmt.Errorf("invalid or duplicate object manifest entry")
		}
		seen[item.Key] = true
		if snapshot.Version == 2 {
			if _, _, err := mime.ParseMediaType(item.ContentType); err != nil {
				return snapshot, fmt.Errorf("invalid content type in object manifest")
			}
		}
		file, err := root.Open("objects/" + item.File)
		if err != nil {
			return snapshot, err
		}
		checksum := sha256.New()
		size, err := io.Copy(checksum, io.LimitReader(file, item.Size+1))
		file.Close()
		if err != nil {
			return snapshot, err
		}
		if size != item.Size || hex.EncodeToString(checksum.Sum(nil)) != item.SHA256 {
			return snapshot, fmt.Errorf("snapshot object failed integrity verification: %s", item.File)
		}
	}
	return snapshot, nil
}

func restoreSnapshot(directory string, snapshot manifest, target string, verifyHTTP, cleanup bool) (result error) {
	if snapshot.Version != 2 {
		return fmt.Errorf("restoring content type and metadata requires a version 2 snapshot; make a new backup")
	}
	if !bucketPattern.MatchString(target) || strings.Contains(target, "..") || target == snapshot.Bucket {
		return fmt.Errorf("restore requires a new valid bucket distinct from the source bucket")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	db, err := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	store, source, err := objectstore.Load(ctx, db.SQL)
	if err != nil {
		return err
	}
	if target == source {
		return fmt.Errorf("restore target cannot be the configured application bucket")
	}
	if verifyHTTP {
		if err := requireRestoreDatabase(ctx, db); err != nil {
			return err
		}
	}
	exists, err := store.BucketExists(ctx, target)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("restore refuses to overwrite an existing bucket")
	}
	if err := store.MakeBucket(ctx, target, minio.MakeBucketOptions{}); err != nil {
		return err
	}
	createdKeys := []string{}
	// This defer is installed only after this invocation created the exact bucket.
	// It removes only keys this invocation successfully uploaded, never a bucket scan.
	if cleanup {
		defer func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
			defer cleanupCancel()
			var cleanupError error
			for _, key := range createdKeys {
				cleanupError = errors.Join(cleanupError, store.RemoveObject(cleanupCtx, target, key, minio.RemoveObjectOptions{}))
			}
			cleanupError = errors.Join(cleanupError, store.RemoveBucket(cleanupCtx, target))
			result = errors.Join(result, cleanupError)
			if cleanupError == nil {
				fmt.Printf("Removed the newly created verification bucket %s.\n", target)
			}
		}()
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, item := range snapshot.Objects {
		file, err := root.Open("objects/" + item.File)
		if err != nil {
			return err
		}
		options := minio.PutObjectOptions{ContentType: item.ContentType, ContentEncoding: item.ContentEncoding, ContentDisposition: item.ContentDisposition, ContentLanguage: item.ContentLanguage, CacheControl: item.CacheControl, UserMetadata: item.Metadata, DisableMultipart: true}
		options.SetMatchETagExcept("*")
		_, err = store.PutObject(ctx, target, item.Key, file, item.Size, options)
		file.Close()
		if err != nil {
			return err
		}
		createdKeys = append(createdKeys, item.Key)
		if err := verifyRestoredObject(ctx, store, target, item); err != nil {
			return err
		}
	}
	fmt.Printf("Restored and verified %d objects, content types and metadata in new bucket %s.\n", len(snapshot.Objects), target)
	if verifyHTTP {
		return verifyApplication(ctx, db, snapshot, target)
	}
	return nil
}

func verifyRestoredObject(ctx context.Context, store *minio.Client, bucket string, item entry) error {
	reader, err := store.GetObject(ctx, bucket, item.Key, minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()
	info, err := reader.Stat()
	if err != nil {
		return err
	}
	if info.Size != item.Size || info.ContentType != item.ContentType || info.ContentEncoding != item.ContentEncoding || info.Metadata.Get("Content-Disposition") != item.ContentDisposition || info.Metadata.Get("Content-Language") != item.ContentLanguage || info.Metadata.Get("Cache-Control") != item.CacheControl || !sameMetadata(info.UserMetadata, item.Metadata) {
		return fmt.Errorf("restored object metadata did not match the snapshot: %s", item.File)
	}
	checksum := sha256.New()
	size, err := io.Copy(checksum, io.LimitReader(reader, item.Size+1))
	if err != nil {
		return err
	}
	if size != item.Size || hex.EncodeToString(checksum.Sum(nil)) != item.SHA256 {
		return fmt.Errorf("restored object content did not match the snapshot: %s", item.File)
	}
	return nil
}

func sameMetadata(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	normalized := map[string]string{}
	for key, value := range a {
		normalized[strings.ToLower(key)] = value
	}
	for key, value := range b {
		if saved, ok := normalized[strings.ToLower(key)]; !ok || saved != value {
			return false
		}
	}
	return true
}
