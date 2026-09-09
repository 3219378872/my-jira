// asset-backup snapshots, verifies, or restores object data into a new bucket.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/minio/minio-go/v7"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/objectstore"
)

type entry struct {
	Key                string            `json:"key"`
	File               string            `json:"file"`
	Size               int64             `json:"size"`
	SHA256             string            `json:"sha256"`
	ContentType        string            `json:"content_type,omitempty"`
	ContentEncoding    string            `json:"content_encoding,omitempty"`
	ContentDisposition string            `json:"content_disposition,omitempty"`
	ContentLanguage    string            `json:"content_language,omitempty"`
	CacheControl       string            `json:"cache_control,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}
type manifest struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Bucket    string    `json:"bucket"`
	Objects   []entry   `json:"objects"`
}

func run() error {
	directory := flag.String("directory", "", "Existing destination directory owned by this backup")
	verify := flag.Bool("verify", false, "Verify a snapshot without accessing its source")
	restore := flag.Bool("restore", false, "Restore the verified snapshot into a new bucket; never overwrite an existing bucket")
	targetBucket := flag.String("bucket", "", "New target bucket required for restore")
	verifyHTTP := flag.Bool("verify-application", false, "Verify restored attachments through authenticated HTTP; requires an isolated myjira_restore_test_ database")
	cleanup := flag.Bool("cleanup", false, "Remove only the target bucket and objects created by this restore after verification")
	flag.Parse()
	if *directory == "" {
		return fmt.Errorf("directory is required")
	}
	root, e := filepath.Abs(*directory)
	if e != nil {
		return e
	}
	if (*verify && *restore) || ((*cleanup || *verifyHTTP || *targetBucket != "") && !*restore) {
		return fmt.Errorf("restore options require -restore; -verify and -restore are mutually exclusive")
	}
	if *verify || *restore {
		snapshot, e := verifySnapshot(root)
		if e != nil {
			return e
		}
		fmt.Printf("Verified %d object checksums.\n", len(snapshot.Objects))
		if *restore {
			return restoreSnapshot(root, snapshot, *targetBucket, *verifyHTTP, *cleanup)
		}
		return nil
	}
	return backupSnapshot(root)
}

func backupSnapshot(root string) error {
	if _, e := os.Stat(root); e != nil {
		return e
	}
	objectDir := filepath.Join(root, "objects")
	if e := os.Mkdir(objectDir, 0700); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	db, e := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		return e
	}
	defer db.Close()
	store, bucket, e := objectstore.Load(ctx, db.SQL)
	if e != nil {
		return e
	}
	snapshot := manifest{Version: 2, CreatedAt: time.Now().UTC(), Bucket: bucket, Objects: []entry{}}
	for object := range store.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if object.Err != nil {
			return object.Err
		}
		keyHash := sha256.Sum256([]byte(object.Key))
		name := hex.EncodeToString(keyHash[:])
		reader, e := store.GetObject(ctx, bucket, object.Key, minio.GetObjectOptions{})
		if e != nil {
			return e
		}
		info, e := reader.Stat()
		if e != nil {
			reader.Close()
			return e
		}
		file, e := os.OpenFile(filepath.Join(objectDir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			reader.Close()
			return e
		}
		hash := sha256.New()
		n, e := io.Copy(io.MultiWriter(file, hash), reader)
		reader.Close()
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		snapshot.Objects = append(snapshot.Objects, entry{Key: object.Key, File: name, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil)), ContentType: info.ContentType, ContentEncoding: info.ContentEncoding, ContentDisposition: info.Metadata.Get("Content-Disposition"), ContentLanguage: info.Metadata.Get("Content-Language"), CacheControl: info.Metadata.Get("Cache-Control"), Metadata: info.UserMetadata})
	}
	file, e := os.OpenFile(filepath.Join(root, "objects.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if e = encoder.Encode(snapshot); e != nil {
		return e
	}
	fmt.Printf("Backed up %d objects.\n", len(snapshot.Objects))
	return nil
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "Backup failed:", e)
		os.Exit(1)
	}
}
