# Tempest InfluxDB API

An HTTP API that serves WeatherFlow Tempest weather data out of InfluxDB, so other applications can query it without speaking Flux. Pairs with [tempest-influxdb](https://github.com/michaelpeterswa/tempest-influxdb), which collects the station's UDP broadcasts into InfluxDB. Structure modeled on [lfpweather-api](https://github.com/michaelpeterswa/lfpweather-api).

## Endpoints

All endpoints live under `/api/v1` and return JSON. Errors are RFC 9457 `application/problem+json`.

For each metric below:

- `GET /api/v1/{metric}/last` — newest value: `{"time": ..., "last": ...}`
- `GET /api/v1/{metric}/{window}` — bucketed history: `[{"time": ..., "min": ..., "max": ..., "avg": ...}, ...]`

Windows and their bucket sizes: `12h` (30m), `24h` (1h), `7d` (6h), `30d` (1d).

| Metric            | InfluxDB Field    |
|-------------------|-------------------|
| `temperature`     | `temp`            |
| `humidity`        | `humidity`        |
| `dew_point`       | `dew_point`       |
| `pressure`        | `p`               |
| `wind_speed`      | `wind_avg`        |
| `wind_gust`       | `wind_gust`       |
| `rain`            | `precipitation`   |
| `solar_radiation` | `solar_radiation` |
| `uv_index`        | `uv`              |
| `illuminance`     | `illuminance`     |
| `strike_count`    | `strike_count`    |
| `battery`         | `battery`         |

## Configuration

All configuration is via environment variables.

| Environment Variable      | Description                                        | Required | Default              |
|---------------------------|----------------------------------------------------|----------|----------------------|
| `INFLUX_URL`              | InfluxDB base URL                                  | Yes      | -                    |
| `INFLUX_TOKEN`            | InfluxDB authentication token (read access)        | Yes      | -                    |
| `INFLUX_ORG`              | InfluxDB organization                              | Yes      | -                    |
| `INFLUX_BUCKET`           | InfluxDB bucket to query                           | Yes      | -                    |
| `INFLUX_MEASUREMENT`      | Measurement name written by the collector          | No       | `weather`            |
| `QUERY_TIMEOUT`           | Timeout for one InfluxDB query                     | No       | `10s`                |
| `DRAGONFLY_HOST`          | Dragonfly (or Redis) host for response caching     | Yes      | -                    |
| `DRAGONFLY_PORT`          | Dragonfly port                                     | No       | `6379`               |
| `DRAGONFLY_AUTH`          | Dragonfly password                                 | No       | -                    |
| `DRAGONFLY_KEY_PREFIX`    | Cache key prefix                                   | No       | `tempest`            |
| `CACHE_RESULTS_DURATION`  | Cache TTL for query results                        | No       | `5m`                 |
| `PORT`                    | HTTP listen port                                   | No       | `8080`               |
| `AUTHENTICATION_ENABLED`  | Require an API key on `/api` routes                | No       | `false`              |
| `API_KEYS`                | Comma-separated list of accepted `X-API-Key` values| No       | -                    |
| `LOG_LEVEL`               | Log level (`debug`, `info`, `warn`, `error`)       | No       | `error`              |
| `METRICS_ENABLED`         | Serve OpenTelemetry metrics                        | No       | `true`               |
| `METRICS_PORT`            | Prometheus metrics port                            | No       | `8081`               |
| `TRACING_ENABLED`         | Enable OpenTelemetry tracing                       | No       | `false`              |
| `TRACING_SAMPLERATE`      | Trace sample rate                                  | No       | `0.01`               |
| `TRACING_SERVICE`         | Trace service name                                 | No       | `tempest-influxdb-api` |
| `TRACING_VERSION`         | Trace service version                              | No       | -                    |

## Examples

### Docker Compose

```yaml
services:
  tempest-influxdb-api:
    image: "ghcr.io/michaelpeterswa/tempest-influxdb-api:latest"
    environment:
      INFLUX_URL: "https://metrics.example.com"
      INFLUX_TOKEN: "SOMEARBITRARYSTRING"
      INFLUX_ORG: "myorg"
      INFLUX_BUCKET: "weather"
      DRAGONFLY_HOST: "dragonfly"
      LOG_LEVEL: "info"
    ports:
      - 8080:8080
      - 8081:8081

  dragonfly:
    image: "docker.dragonflydb.io/dragonflydb/dragonfly:latest"
    ulimits:
      memlock: -1
```

### Query

```console
$ curl -s localhost:8080/api/v1/temperature/last
{"time":"2026-08-15T19:37:00Z","last":25.5}

$ curl -s localhost:8080/api/v1/temperature/24h
[{"time":"2026-08-15T00:00:00Z","min":18.2,"max":24.9,"avg":21.3}, ...]
```

## License

MIT License - see [LICENSE](LICENSE) file.
