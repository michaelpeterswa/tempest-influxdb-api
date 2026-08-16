package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/michaelpeterswa/tempest-influxdb-api/internal/influxdb"
)

// Metric maps an API metric name to its InfluxDB field.
type Metric struct {
	Name  string
	Field string
}

// Metrics is the set of Tempest fields the API serves, in route order.
var Metrics = []Metric{
	{Name: "temperature", Field: "temp"},
	{Name: "humidity", Field: "humidity"},
	{Name: "dew_point", Field: "dew_point"},
	{Name: "pressure", Field: "p"},
	{Name: "wind_speed", Field: "wind_avg"},
	{Name: "wind_gust", Field: "wind_gust"},
	{Name: "wind_direction", Field: "wind_direction"},
	{Name: "rain", Field: "precipitation"},
	{Name: "solar_radiation", Field: "solar_radiation"},
	{Name: "uv_index", Field: "uv"},
	{Name: "illuminance", Field: "illuminance"},
	{Name: "strike_count", Field: "strike_count"},
	{Name: "strike_distance", Field: "strike_distance"},
	{Name: "battery", Field: "battery"},
}

// Window is one lookback tier with its bucket size.
type Window struct {
	Name             string
	LookbackInterval string
	TimeBucket       string
}

// Windows mirrors the lfpweather-api tiers: the bucket grows with the
// lookback so the point count stays bounded.
var Windows = []Window{
	{Name: "12h", LookbackInterval: "12h", TimeBucket: "30m"},
	{Name: "24h", LookbackInterval: "24h", TimeBucket: "1h"},
	{Name: "7d", LookbackInterval: "7d", TimeBucket: "6h"},
	{Name: "30d", LookbackInterval: "30d", TimeBucket: "1d"},
	{Name: "90d", LookbackInterval: "90d", TimeBucket: "1d"},
}

// lastLookback bounds the search for the newest point; the Tempest reports
// every minute, so a day of slack is plenty.
const lastLookback = "24h"

type WeatherHandler struct {
	influxClient *influxdb.InfluxClient
}

func NewWeatherHandler(influxClient *influxdb.InfluxClient) *WeatherHandler {
	return &WeatherHandler{influxClient: influxClient}
}

func (s *WeatherHandler) Close() {
	s.influxClient.Close()
}

// GetFieldWindow returns the handler for one metric and window tier.
func (s *WeatherHandler) GetFieldWindow(metric Metric, window Window) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values, err := s.influxClient.GetField(r.Context(), s.influxClient.FieldParameters(metric.Field, window.TimeBucket, window.LookbackInterval))
		if err != nil {
			writeProblem(w, r, http.StatusInternalServerError,
				fmt.Sprintf("failed to get %s data", window.Name),
				fmt.Sprintf("error getting data for field %s: %s", metric.Field, err.Error()))
			return
		}

		writeJSON(w, r, metric.Field, values)
	}
}

// GetFieldLast returns the handler for one metric's newest value.
func (s *WeatherHandler) GetFieldLast(metric Metric) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		value, err := s.influxClient.GetFieldLast(r.Context(), s.influxClient.FieldLastParameters(metric.Field, lastLookback))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, influxdb.ErrNoData) {
				status = http.StatusNotFound
			}
			writeProblem(w, r, status,
				"failed to get last data",
				fmt.Sprintf("error getting data for field %s: %s", metric.Field, err.Error()))
			return
		}

		writeJSON(w, r, metric.Field, value)
	}
}

// writeJSON marshals and writes a success response.
func writeJSON(w http.ResponseWriter, r *http.Request, field string, v any) {
	res, err := json.Marshal(v)
	if err != nil {
		writeProblem(w, r, http.StatusInternalServerError,
			"failed to marshal data",
			fmt.Sprintf("error marshalling data for field %s: %s", field, err.Error()))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(res); err != nil {
		writeProblem(w, r, http.StatusInternalServerError,
			"failed to write data",
			fmt.Sprintf("error writing data for field %s: %s", field, err.Error()))
	}
}
