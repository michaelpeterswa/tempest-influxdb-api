package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob" // file:// for local runs
	_ "gocloud.dev/blob/gcsblob"  // gs:// via Application Default Credentials

	"github.com/michaelpeterswa/tempest-influxdb-api/internal/config"
	"github.com/michaelpeterswa/tempest-influxdb-api/internal/influxdb"
	"github.com/michaelpeterswa/tempest-influxdb-api/internal/publish"
)

// runPublish computes one snapshot and uploads it to the bucket. It is meant
// to run as a cron job: one process, one snapshot, exit.
func runPublish(ctx context.Context, c *config.Config) error {
	if c.PublishBucketURL == "" {
		return errors.New("PUBLISH_BUCKET_URL is required for publish")
	}

	// No dragonfly here: the publisher wants fresh data, not the API's
	// response cache, and a cron job should not depend on the cache being up.
	influxClient, err := influxdb.NewInfluxClient(ctx, c.InfluxURL, c.InfluxToken, c.InfluxOrg, c.InfluxBucket, c.InfluxMeasurement, c.QueryTimeout)
	if err != nil {
		return fmt.Errorf("create influxdb client: %w", err)
	}
	defer influxClient.Close()

	bucket, err := blob.OpenBucket(ctx, c.PublishBucketURL)
	if err != nil {
		return fmt.Errorf("open bucket %s: %w", c.PublishBucketURL, err)
	}
	defer func() { _ = bucket.Close() }()

	publisher := &publish.Publisher{
		Reader:       influxClient,
		Bucket:       bucket,
		Prefix:       c.PublishPrefix,
		CacheControl: c.PublishCacheControl,
	}

	slog.Info("publishing snapshot", slog.String("bucket", c.PublishBucketURL), slog.String("prefix", c.PublishPrefix))
	return publisher.Run(ctx)
}
