// Package objectstore resolves current instance settings for every operation.
package objectstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/serviceconfig"
)

func Load(ctx context.Context, q database.DBTX) (*minio.Client, string, error) {
	values, err := serviceconfig.Load(ctx, q, "storage")
	if err != nil {
		return nil, "", err
	}
	endpoint, bucket := values.String("endpoint"), values.String("bucket")
	if endpoint == "" || bucket == "" || values.String("access_key") == "" || values.String("secret_key") == "" {
		return nil, "", fmt.Errorf("object storage has not been configured")
	}
	if strings.Contains(endpoint, "://") {
		return nil, "", fmt.Errorf("storage endpoint must be a host and optional port")
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(values.String("access_key"), values.String("secret_key"), ""),
		Secure: values.Bool("secure"), Region: values.String("region"),
	})
	return client, bucket, err
}

func EnsureBucket(ctx context.Context, client *minio.Client, bucket string) error {
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil || exists {
		return err
	}
	err = client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
	if code := minio.ToErrorResponse(err).Code; code == "BucketAlreadyOwnedByYou" {
		return nil
	}
	return err
}
