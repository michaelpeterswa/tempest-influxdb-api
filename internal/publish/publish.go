// Package publish precomputes the API's responses and uploads them to a
// public bucket. The station sits behind a finite tower uplink, so the tower
// pushes each snapshot once and however many readers there are pull from the
// bucket instead of from the tower.
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gocloud.dev/blob"

	"github.com/michaelpeterswa/tempest-influxdb-api/internal/handlers"
	"github.com/michaelpeterswa/tempest-influxdb-api/internal/influxdb"
)

// FieldReader is the slice of the influxdb client the publisher needs, so
// tests can substitute a fake.
type FieldReader interface {
	FieldParameters(field string, timeBucket string, lookbackInterval string) influxdb.GetFieldTemplateParameters
	FieldLastParameters(field string, lookbackInterval string) influxdb.GetFieldLastTemplateParameters
	GetField(ctx context.Context, tp influxdb.GetFieldTemplateParameters) ([]influxdb.GetFieldResponse, error)
	GetFieldLast(ctx context.Context, tp influxdb.GetFieldLastTemplateParameters) (*influxdb.GetFieldLastResponse, error)
}

// Publisher computes one snapshot and writes it to the bucket.
type Publisher struct {
	Reader       FieldReader
	Bucket       *blob.Bucket
	Prefix       string
	CacheControl string
}

// MetricSnapshot is one metric's precomputed responses: the newest reading and
// every window, keyed by window name. The shapes match the HTTP API exactly so
// a consumer can switch between the two without translation.
type MetricSnapshot struct {
	Last    *influxdb.GetFieldLastResponse         `json:"last,omitempty"`
	Windows map[string][]influxdb.GetFieldResponse `json:"windows"`
}

// Snapshot is the combined document: every metric in one object so a static
// site can hydrate a whole dashboard with a single request.
type Snapshot struct {
	GeneratedAt time.Time                 `json:"generated_at"`
	Metrics     map[string]MetricSnapshot `json:"metrics"`
}

// Run computes and uploads the snapshot: one object per endpoint
// ({prefix}/{metric}/last.json, {prefix}/{metric}/{window}.json) plus the
// combined {prefix}/snapshot.json.
func (p *Publisher) Run(ctx context.Context) error {
	snapshot := Snapshot{
		GeneratedAt: time.Now().UTC(),
		Metrics:     make(map[string]MetricSnapshot),
	}

	for _, metric := range handlers.Metrics {
		ms := MetricSnapshot{Windows: make(map[string][]influxdb.GetFieldResponse)}

		last, err := p.Reader.GetFieldLast(ctx, p.Reader.FieldLastParameters(metric.Field, handlers.LastLookback))
		if err != nil && !errors.Is(err, influxdb.ErrNoData) {
			return fmt.Errorf("get last %s: %w", metric.Name, err)
		}
		ms.Last = last

		if last != nil {
			if err := p.put(ctx, fmt.Sprintf("%s/%s/last.json", p.Prefix, metric.Name), last); err != nil {
				return err
			}
		}

		for _, window := range handlers.Windows {
			points, err := p.Reader.GetField(ctx, p.Reader.FieldParameters(metric.Field, window.TimeBucket, window.LookbackInterval))
			if err != nil {
				return fmt.Errorf("get %s %s: %w", metric.Name, window.Name, err)
			}
			if points == nil {
				points = []influxdb.GetFieldResponse{}
			}
			ms.Windows[window.Name] = points

			if err := p.put(ctx, fmt.Sprintf("%s/%s/%s.json", p.Prefix, metric.Name, window.Name), points); err != nil {
				return err
			}
		}

		snapshot.Metrics[metric.Name] = ms
	}

	if err := p.put(ctx, fmt.Sprintf("%s/snapshot.json", p.Prefix), snapshot); err != nil {
		return err
	}

	slog.Info("snapshot published",
		slog.Int("metrics", len(snapshot.Metrics)),
		slog.Time("generated_at", snapshot.GeneratedAt))
	return nil
}

// put marshals and uploads one object. A blob write is only visible once
// complete, so readers see the previous version or the new one, never a
// partial document.
func (p *Publisher) put(ctx context.Context, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}

	err = p.Bucket.WriteAll(ctx, key, data, &blob.WriterOptions{
		ContentType:  "application/json",
		CacheControl: p.CacheControl,
	})
	if err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	return nil
}
