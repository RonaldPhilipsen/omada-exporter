# omada-exporter

omada-exporter polls a TP-Link Omada controller and pushes the result as OpenTelemetry metrics. The service is written in Go. It reads the controller through the Omada OpenAPI with the [github.com/RonaldPhilipsen/omada](https://github.com/RonaldPhilipsen/omada) module. The project ships a container image to [GHCR](https://ghcr.io/ronaldphilipsen/omada-exporter) and a Grafana dashboard in `dashboard.json`.

The service pushes metrics over OTLP, the OpenTelemetry protocol. It does not serve a scrape endpoint. Any OTLP receiver accepts the metrics. This document uses Prometheus with its OTLP receiver as the example.

## Authentication

The service uses client credentials, an ID and a secret that identify the application. You create the application in the Omada controller. The service never handles usernames and passwords. It does not support the hosted Omada cloud mode.

## Configuration

All configuration uses environment variables. The service reads no configuration files. The four required variables name the controller and the OpenAPI application. The optional variables set TLS, the poll intervals, and the client metrics.

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `OMADA_BASE_URL` | yes | | Controller address, for example `https://omada.example.com:8043` |
| `OMADA_OMADAC_ID` | yes | | Controller ID |
| `OMADA_CLIENT_ID` | yes | | OpenAPI application client ID |
| `OMADA_CLIENT_SECRET` | yes | | OpenAPI application client secret |
| `OMADA_INSECURE_TLS` | no | `false` | Set to `true` to accept a self-signed controller certificate |
| `OMADA_POLL_INTERVAL` | no | `60s` | Time between polls of the controller, for example `30s` |
| `OMADA_FIRMWARE_POLL_INTERVAL` | no | `1h` | Time between firmware checks for each device |
| `OMADA_CLIENT_METRICS` | no | `true` | Set to `false` to remove every `omada.client.*` metric |

The poll intervals use the Go duration format. If a value is not valid, the service stops at start-up with a message that names the variable.

## OTLP export

The service reads the standard OpenTelemetry environment variables. The default endpoint is `http://localhost:4318`. If your receiver runs at another address, set the endpoint.

| Variable | Default | Purpose |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://localhost:4318` | Address of the OTLP receiver |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` | Transport: `http/protobuf` or `grpc` |
| `OTEL_METRIC_EXPORT_INTERVAL` | `60000` | Time between exports, in milliseconds |

The Prometheus OTLP receiver listens on the path `/api/v1/otlp`. If you use Prometheus, start it with `--web.enable-otlp-receiver` and set these values:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://prometheus:9090/api/v1/otlp
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

The resource carries `service.name=omada-exporter` and `service.version`. Every series except the `omada.controller.*` and `omada.exporter.*` series carries the attribute `omada.site.name`.

## Metrics

The table lists each metric with its OpenTelemetry name and its name in Prometheus with the default translation. A `direction` attribute has the value `transmit` or `receive`.

| Metric | Type | Prometheus name | Attributes |
|---|---|---|---|
| `omada.controller.uptime` | gauge | `omada_controller_uptime_seconds` | |
| `omada.controller.storage.used` | gauge | `omada_controller_storage_used_bytes` | `storage.name` |
| `omada.controller.storage.capacity` | gauge | `omada_controller_storage_capacity_bytes` | `storage.name` |
| `omada.device.cpu.utilization` | gauge | `omada_device_cpu_utilization_ratio` | device |
| `omada.device.memory.utilization` | gauge | `omada_device_memory_utilization_ratio` | device |
| `omada.device.status` | gauge | `omada_device_status_ratio` | device |
| `omada.device.uptime` | gauge | `omada_device_uptime_seconds` | device |
| `omada.device.upgrade.available` | gauge | `omada_device_upgrade_available_ratio` | device |
| `omada.switch.port.io` | counter | `omada_switch_port_io_bytes_total` | port, `direction` |
| `omada.switch.port.packets` | counter | `omada_switch_port_packets_total` | port, `direction` |
| `omada.switch.port.errors` | counter | `omada_switch_port_errors_total` | port, `direction` |
| `omada.switch.port.speed` | gauge | `omada_switch_port_speed_Mbit_per_second` | port |
| `omada.switch.port.connected` | gauge | `omada_switch_port_connected_ratio` | port |
| `omada.switch.port.poe.power` | gauge | `omada_switch_port_poe_power_watts` | port |
| `omada.site.clients` | gauge | `omada_site_clients` | `client.connection` |
| `omada.client.signal.strength` | gauge | `omada_client_signal_strength_dBm` | client |
| `omada.client.uptime` | gauge | `omada_client_uptime_seconds` | client |
| `omada.client.traffic` | counter | `omada_client_traffic_bytes_total` | client, `direction` |
| `omada.client.activity` | gauge | `omada_client_activity_bytes_per_second` | client |
| `omada.exporter.scrape.errors` | counter | `omada_exporter_scrape_errors_total` | `section` |
| `omada.exporter.scrape.duration` | gauge | `omada_exporter_scrape_duration_seconds` | |

The attribute groups have these members:

- Device: `device.mac`, `device.name`, `device.model`, `device.type`
- Port: `switch.mac`, `switch.name`, `port.number`, `port.name`. Ports of a stacked switch also carry `port.unit`, the stack unit number.
- Client: `client.mac`, `client.name`, `client.connection` (`wired` or `wireless`). Wireless clients also carry `client.ssid` and `client.ap.name`.

Prometheus replaces each dot in an attribute name with the `_` character. Examples are `omada_site_name` and `device_name`.

Note these points about the values:

- If the controller does not report `hwcStorage`, the `omada.controller.storage.*` series do not exist.
- The Omada API reports device uptime as text, for example `10day(s) 23h 51m 36s`. The service parses the text, so `omada.device.uptime` has coarse resolution.
- The service treats the storage values from the controller as gigabytes and converts them to bytes.
- If a field is absent from a controller response, the service reports no value. It does not report zero.
- `omada.device.upgrade.available` has no value until the first firmware check for the device finishes.
- Counters carry the value from the controller. If a client reconnects, the controller value can decrease, and the backend treats the decrease as a counter reset.
- `omada.exporter.scrape.errors` appears after the first scrape error. We tested the other names on Prometheus with a fake Omada server. We did not test this name, so it follows the translation rules only.

## Dashboard

The file `dashboard.json` is a Grafana dashboard for a Prometheus data source. Import it in Grafana. The dashboard has the variables `Datasource`, `Site`, and `Device`. It shows these items: site totals, CPU and memory use of the devices, a table of device details, port speed and PoE power, and the most active clients.

The dashboard targets the default Prometheus translation of OTLP names, which keeps unit suffixes. This translation is the Prometheus setting `translation_strategy: UnderscoreEscapingWithSuffixes` in the `otlp` section of `prometheus.yml`:

```yaml
otlp:
  translation_strategy: UnderscoreEscapingWithSuffixes
```

If you use another translation strategy, the metric names differ and the dashboard queries find no data. Resource attributes are not labels by default. No dashboard query uses them.

## Running the service

Run the image from GHCR. The example sends metrics to a Prometheus server. The name `prometheus` must resolve from inside the container, for example when both containers share a Docker network.

```bash
docker run --rm \
  -e OMADA_BASE_URL=https://omada.example.com:8043 \
  -e OMADA_OMADAC_ID=abc123 \
  -e OMADA_CLIENT_ID=your-client-id \
  -e OMADA_CLIENT_SECRET=your-client-secret \
  -e OTEL_EXPORTER_OTLP_ENDPOINT=http://prometheus:9090/api/v1/otlp \
  -e OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
  ghcr.io/ronaldphilipsen/omada-exporter:<version>
```

Replace `<version>` with a release tag. The image has only version tags and no `latest` tag. If the controller uses a self-signed certificate, add `OMADA_INSECURE_TLS=true`. The service writes logs to stderr. Run `omada-exporter -version` to print the version.

## Building from source

You need Go 1.27.3. Run `go build ./cmd/omada-exporter` to build the binary. Run `go test ./... -race -count=1` to run the test suite. The tests need no network.

## Development

Install the toolchain with mise. The project pins Go 1.27.3 and prek in `mise.toml`. The command `mise install` also installs the pre-commit hooks.

Commit messages use the conventional commit format, because semVersie calculates the version from them.

## Releases

If new commits on `main` produce a new version, GitHub Actions creates a release. The workflow calculates the version from the commit history with semVersie. It builds the image for linux/amd64 and linux/arm64. It pushes only the version tag to GHCR and creates a GitHub release with the release notes.
