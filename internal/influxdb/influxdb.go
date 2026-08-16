// Package influxdb reads the Tempest weather series out of InfluxDB with Flux
// queries and caches the results in Dragonfly.
package influxdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"text/template"
	"time"

	_ "embed"

	"github.com/cespare/xxhash/v2"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	influxdb2api "github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/michaelpeterswa/tempest-influxdb-api/internal/dragonfly"
	"github.com/redis/go-redis/v9"
)

type InfluxClient struct {
	Client   influxdb2.Client
	QueryAPI influxdb2api.QueryAPI
	Dfly     *dragonfly.DragonflyClient

	bucket       string
	measurement  string
	queryTimeout time.Duration

	getFieldTemplate     *template.Template
	getFieldLastTemplate *template.Template
}

//go:embed queries/getfield.flux.gotmpl
var getFieldTemplate string

//go:embed queries/getfieldlast.flux.gotmpl
var getFieldLastTemplate string

// GetFieldTemplateParameters selects a field and how to bucket it. The values
// come from the server-side metric table, never from request input, so they
// can be interpolated into the Flux template safely.
type GetFieldTemplateParameters struct {
	Bucket           string
	Measurement      string
	Field            string
	TimeBucket       string
	LookbackInterval string
}

func (t *GetFieldTemplateParameters) String() string {
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		strings.ReplaceAll(t.Bucket, " ", ""),
		strings.ReplaceAll(t.Measurement, " ", ""),
		strings.ReplaceAll(t.Field, " ", ""),
		strings.ReplaceAll(t.TimeBucket, " ", ""),
		strings.ReplaceAll(t.LookbackInterval, " ", ""))
}

type GetFieldLastTemplateParameters struct {
	Bucket           string
	Measurement      string
	Field            string
	LookbackInterval string
}

func (t *GetFieldLastTemplateParameters) String() string {
	return fmt.Sprintf("%s-%s-%s-last",
		strings.ReplaceAll(t.Bucket, " ", ""),
		strings.ReplaceAll(t.Measurement, " ", ""),
		strings.ReplaceAll(t.Field, " ", ""))
}

func (t *GetFieldTemplateParameters) Hash() string {
	return strconv.FormatUint(xxhash.Sum64String(t.String()), 16)
}

func (t *GetFieldLastTemplateParameters) Hash() string {
	return strconv.FormatUint(xxhash.Sum64String(t.String()), 16)
}

type GetFieldResponse struct {
	Time time.Time `json:"time"`
	Min  float64   `json:"min"`
	Max  float64   `json:"max"`
	Avg  float64   `json:"avg"`
}

type GetFieldLastResponse struct {
	Time time.Time `json:"time"`
	Last float64   `json:"last"`
}

type InfluxClientOption func(*InfluxClient)

func WithDragonflyClient(dfly *dragonfly.DragonflyClient) InfluxClientOption {
	return func(c *InfluxClient) {
		c.Dfly = dfly
	}
}

// NewInfluxClient connects to InfluxDB and pings it.
func NewInfluxClient(ctx context.Context, url string, token string, org string, bucket string, measurement string, queryTimeout time.Duration, opts ...InfluxClientOption) (*InfluxClient, error) {
	client := influxdb2.NewClient(url, token)

	pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
	defer pingCancel()

	ok, err := client.Ping(pingCtx)
	if err != nil {
		return nil, fmt.Errorf("ping influxdb: %w", err)
	}
	if !ok {
		return nil, errors.New("influxdb ping returned not ok")
	}

	getField, err := template.New("getfield").Parse(getFieldTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse getfield template: %w", err)
	}
	getFieldLast, err := template.New("getfieldlast").Parse(getFieldLastTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse getfieldlast template: %w", err)
	}

	c := &InfluxClient{
		Client:               client,
		QueryAPI:             client.QueryAPI(org),
		bucket:               bucket,
		measurement:          measurement,
		queryTimeout:         queryTimeout,
		getFieldTemplate:     getField,
		getFieldLastTemplate: getFieldLast,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

func (c *InfluxClient) Close() {
	c.Client.Close()
}

// FieldParameters fills in the bucket and measurement for a field query.
func (c *InfluxClient) FieldParameters(field string, timeBucket string, lookbackInterval string) GetFieldTemplateParameters {
	return GetFieldTemplateParameters{
		Bucket:           c.bucket,
		Measurement:      c.measurement,
		Field:            field,
		TimeBucket:       timeBucket,
		LookbackInterval: lookbackInterval,
	}
}

// FieldLastParameters fills in the bucket and measurement for a last-value
// query. The lookback bounds how far back the search for the newest point
// goes, because an unbounded Flux range scan is expensive.
func (c *InfluxClient) FieldLastParameters(field string, lookbackInterval string) GetFieldLastTemplateParameters {
	return GetFieldLastTemplateParameters{
		Bucket:           c.bucket,
		Measurement:      c.measurement,
		Field:            field,
		LookbackInterval: lookbackInterval,
	}
}

// GetField returns min/max/avg buckets for a field over the lookback interval.
func (c *InfluxClient) GetField(ctx context.Context, tp GetFieldTemplateParameters) ([]GetFieldResponse, error) {
	if c.Dfly != nil {
		res, err := c.Dfly.GetClient().Get(ctx, fmt.Sprintf("%s-%s", c.Dfly.KeyPrefix, tp.Hash())).Result()
		if err == nil {
			var getFieldResponses []GetFieldResponse
			err := json.Unmarshal([]byte(res), &getFieldResponses)
			if err != nil {
				slog.Error("failed to unmarshal from dragonfly", slog.String("error", err.Error()))
			}
			return getFieldResponses, nil
		} else if !errors.Is(err, redis.Nil) {
			slog.Error("failed to get from dragonfly", slog.String("error", err.Error()))
		}
	}

	query := bytes.NewBuffer(nil)
	err := c.getFieldTemplate.Execute(query, tp)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query template: %w", err)
	}

	slog.Debug("query", slog.String("query", query.String()))

	queryCtx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	result, err := c.QueryAPI.Query(queryCtx, query.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get %s for the last %s: %w", tp.Field, tp.LookbackInterval, err)
	}

	var getFieldResponses []GetFieldResponse

	for result.Next() {
		record := result.Record()

		row := GetFieldResponse{Time: record.Time()}
		var ok bool
		if row.Min, ok = floatValue(record.ValueByKey("min")); !ok {
			continue
		}
		if row.Max, ok = floatValue(record.ValueByKey("max")); !ok {
			continue
		}
		if row.Avg, ok = floatValue(record.ValueByKey("avg")); !ok {
			continue
		}

		getFieldResponses = append(getFieldResponses, row)
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("failed to read %s query result: %w", tp.Field, result.Err())
	}

	if c.Dfly != nil {
		getFieldResponsesJSON, err := json.Marshal(getFieldResponses)
		if err != nil {
			slog.Error("failed to marshal to dragonfly", slog.String("error", err.Error()))
		} else {
			err := c.Dfly.GetClient().Set(ctx, fmt.Sprintf("%s-%s", c.Dfly.KeyPrefix, tp.Hash()), getFieldResponsesJSON, c.Dfly.CacheResultsDuration).Err()
			if err != nil {
				slog.Error("failed to set to dragonfly", slog.String("error", err.Error()))
			}
		}
	}

	return getFieldResponses, nil
}

var ErrNoData = errors.New("no data for field")

// GetFieldLast returns the newest value for a field.
func (c *InfluxClient) GetFieldLast(ctx context.Context, tp GetFieldLastTemplateParameters) (*GetFieldLastResponse, error) {
	if c.Dfly != nil {
		res, err := c.Dfly.GetClient().Get(ctx, fmt.Sprintf("%s-%s", c.Dfly.KeyPrefix, tp.Hash())).Result()
		if err == nil {
			var getFieldLastResponse GetFieldLastResponse
			err := json.Unmarshal([]byte(res), &getFieldLastResponse)
			if err != nil {
				slog.Error("failed to unmarshal from dragonfly", slog.String("error", err.Error()))
			}
			return &getFieldLastResponse, nil
		} else if !errors.Is(err, redis.Nil) {
			slog.Error("failed to get from dragonfly", slog.String("error", err.Error()))
		}
	}

	query := bytes.NewBuffer(nil)
	err := c.getFieldLastTemplate.Execute(query, tp)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query template: %w", err)
	}

	slog.Debug("query", slog.String("query", query.String()))

	queryCtx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	result, err := c.QueryAPI.Query(queryCtx, query.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get %s: %w", tp.Field, err)
	}

	var getFieldLastResponse *GetFieldLastResponse

	for result.Next() {
		record := result.Record()
		value, ok := floatValue(record.Value())
		if !ok {
			continue
		}
		getFieldLastResponse = &GetFieldLastResponse{
			Time: record.Time(),
			Last: value,
		}
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("failed to read %s query result: %w", tp.Field, result.Err())
	}
	if getFieldLastResponse == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoData, tp.Field)
	}

	if c.Dfly != nil {
		getFieldLastResponseJSON, err := json.Marshal(getFieldLastResponse)
		if err != nil {
			slog.Error("failed to marshal to dragonfly", slog.String("error", err.Error()))
		} else {
			err := c.Dfly.GetClient().Set(ctx, fmt.Sprintf("%s-%s", c.Dfly.KeyPrefix, tp.Hash()), getFieldLastResponseJSON, c.Dfly.CacheResultsDuration).Err()
			if err != nil {
				slog.Error("failed to set to dragonfly", slog.String("error", err.Error()))
			}
		}
	}

	return getFieldLastResponse, nil
}

// floatValue converts a Flux record value to a float64. Integer-typed points
// can appear if the schema ever changes, so both are accepted.
func floatValue(v any) (float64, bool) {
	switch value := v.(type) {
	case float64:
		return value, true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
}
