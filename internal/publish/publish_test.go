package publish

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/michaelpeterswa/tempest-influxdb-api/internal/handlers"
	"github.com/michaelpeterswa/tempest-influxdb-api/internal/influxdb"
)

type fakeReader struct{}

func (f *fakeReader) FieldParameters(field, timeBucket, lookbackInterval string) influxdb.GetFieldTemplateParameters {
	return influxdb.GetFieldTemplateParameters{Field: field, TimeBucket: timeBucket, LookbackInterval: lookbackInterval}
}

func (f *fakeReader) FieldLastParameters(field, lookbackInterval string) influxdb.GetFieldLastTemplateParameters {
	return influxdb.GetFieldLastTemplateParameters{Field: field, LookbackInterval: lookbackInterval}
}

func (f *fakeReader) GetField(_ context.Context, tp influxdb.GetFieldTemplateParameters) ([]influxdb.GetFieldResponse, error) {
	return []influxdb.GetFieldResponse{
		{Time: time.Unix(1700000000, 0).UTC(), Min: 1, Max: 3, Avg: 2, Sum: 6},
	}, nil
}

func (f *fakeReader) GetFieldLast(_ context.Context, tp influxdb.GetFieldLastTemplateParameters) (*influxdb.GetFieldLastResponse, error) {
	return &influxdb.GetFieldLastResponse{Time: time.Unix(1700000060, 0).UTC(), Last: 2.5}, nil
}

func TestRunPublishesEveryEndpointAndSnapshot(t *testing.T) {
	bucket := memblob.OpenBucket(nil)
	defer func() { _ = bucket.Close() }()

	p := &Publisher{
		Reader:       &fakeReader{},
		Bucket:       bucket,
		Prefix:       "v1",
		CacheControl: "public, max-age=60",
	}

	ctx := context.Background()
	if err := p.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// every per-endpoint object exists
	for _, metric := range handlers.Metrics {
		for _, key := range []string{
			"v1/" + metric.Name + "/last.json",
			"v1/" + metric.Name + "/24h.json",
		} {
			exists, err := bucket.Exists(ctx, key)
			if err != nil || !exists {
				t.Errorf("expected %s to exist (err=%v)", key, err)
			}
		}
	}

	// per-endpoint objects are wrapped in an envelope carrying the query time
	envData, err := bucket.ReadAll(ctx, "v1/temperature/24h.json")
	if err != nil {
		t.Fatalf("read temperature/24h: %v", err)
	}
	var envelope struct {
		GeneratedAt time.Time                   `json:"generated_at"`
		Data        []influxdb.GetFieldResponse `json:"data"`
	}
	if err := json.Unmarshal(envData, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope.GeneratedAt.IsZero() {
		t.Error("expected envelope generated_at to be set")
	}
	if len(envelope.Data) != 1 || envelope.Data[0].Avg != 2 {
		t.Errorf("unexpected envelope data %+v", envelope.Data)
	}

	// snapshot decodes and contains every metric with every window
	data, err := bucket.ReadAll(ctx, "v1/snapshot.json")
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if snapshot.GeneratedAt.IsZero() {
		t.Error("expected generated_at to be set")
	}
	if !snapshot.GeneratedAt.Equal(envelope.GeneratedAt) {
		t.Errorf("snapshot generated_at %v differs from envelope %v", snapshot.GeneratedAt, envelope.GeneratedAt)
	}
	if len(snapshot.Metrics) != len(handlers.Metrics) {
		t.Errorf("expected %d metrics in snapshot, got %d", len(handlers.Metrics), len(snapshot.Metrics))
	}
	for name, ms := range snapshot.Metrics {
		if len(ms.Windows) != len(handlers.Windows) {
			t.Errorf("metric %s: expected %d windows, got %d", name, len(handlers.Windows), len(ms.Windows))
		}
		if ms.Last == nil || ms.Last.Last != 2.5 {
			t.Errorf("metric %s: unexpected last %+v", name, ms.Last)
		}
	}

	// content type and cache control land on the object
	attrs, err := bucket.Attributes(ctx, "v1/snapshot.json")
	if err != nil {
		t.Fatalf("attributes: %v", err)
	}
	if attrs.ContentType != "application/json" {
		t.Errorf("expected content type application/json, got %q", attrs.ContentType)
	}
	if attrs.CacheControl != "public, max-age=60" {
		t.Errorf("expected cache control 'public, max-age=60', got %q", attrs.CacheControl)
	}
}
