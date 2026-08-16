package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"alpineworks.io/ootel"
	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"

	"github.com/michaelpeterswa/tempest-influxdb-api/internal/config"
	"github.com/michaelpeterswa/tempest-influxdb-api/internal/dragonfly"
	"github.com/michaelpeterswa/tempest-influxdb-api/internal/handlers"
	"github.com/michaelpeterswa/tempest-influxdb-api/internal/influxdb"
	"github.com/michaelpeterswa/tempest-influxdb-api/internal/logging"
	"github.com/michaelpeterswa/tempest-influxdb-api/internal/middleware"
)

func main() {
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "error"
	}

	slogLevel, err := logging.LogLevelToSlogLevel(logLevel)
	if err != nil {
		log.Fatalf("could not convert log level: %s", err)
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slogLevel,
	})))

	slog.Info("welcome to tempest-influxdb-api!")

	c, err := config.NewConfig()
	if err != nil {
		slog.Error("could not create config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	ctx := context.Background()

	exporterType := ootel.ExporterTypePrometheus
	if c.Local {
		exporterType = ootel.ExporterTypeOTLPGRPC
	}

	ootelClient := ootel.NewOotelClient(
		ootel.WithMetricConfig(
			ootel.NewMetricConfig(
				c.MetricsEnabled,
				exporterType,
				c.MetricsPort,
			),
		),
		ootel.WithTraceConfig(
			ootel.NewTraceConfig(
				c.TracingEnabled,
				c.TracingSampleRate,
				c.TracingService,
				c.TracingVersion,
			),
		),
	)

	shutdown, err := ootelClient.Init(ctx)
	if err != nil {
		slog.Error("could not create ootel client", slog.String("error", err.Error()))
		os.Exit(1)
	}

	defer func() {
		_ = shutdown(ctx)
	}()

	dragonflyClient, err := dragonfly.NewDragonflyClient(c.DragonflyHost, c.DragonflyPort, c.DragonflyAuth, c.CacheResultsDuration, c.DragonflyKeyPrefix)
	if err != nil {
		slog.Error("error initializing dragonfly client", slog.String("error", err.Error()))
		os.Exit(1)
	}

	influxClient, err := influxdb.NewInfluxClient(ctx, c.InfluxURL, c.InfluxToken, c.InfluxOrg, c.InfluxBucket, c.InfluxMeasurement, c.QueryTimeout,
		influxdb.WithDragonflyClient(dragonflyClient),
	)
	if err != nil {
		slog.Error("could not create influxdb client", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer influxClient.Close()

	weatherHandler := handlers.NewWeatherHandler(influxClient)

	r := mux.NewRouter()
	// Extract the incoming trace context and span each request, so callers'
	// traces continue into this service. A no-op when tracing is disabled.
	r.Use(otelmux.Middleware(c.TracingService))
	apiRouter := r.PathPrefix("/api").Subrouter()
	v1Subrouter := apiRouter.PathPrefix("/v1").Subrouter()

	// last data and windowed min/max/avg buckets for every metric
	for _, metric := range handlers.Metrics {
		v1Subrouter.HandleFunc(fmt.Sprintf("/%s/last", metric.Name), weatherHandler.GetFieldLast(metric)).Methods(http.MethodGet)
		for _, window := range handlers.Windows {
			v1Subrouter.HandleFunc(fmt.Sprintf("/%s/%s", metric.Name, window.Name), weatherHandler.GetFieldWindow(metric, window)).Methods(http.MethodGet)
		}
	}

	if c.AuthenticationEnabled {
		authenticationMiddleware := middleware.NewAuthenticationMiddlewareClient(
			middleware.WithAPIKeys(c.APIKeys),
		)
		apiRouter.Use(authenticationMiddleware.AuthenticationMiddleware)
	}

	http.Handle("/", r)

	err = http.ListenAndServe(fmt.Sprintf(":%d", c.Port), nil)
	if err != nil {
		slog.Error("could not start http server", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
