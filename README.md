# OpenTelemetry Collector Custom Processor Demo

This project demonstrates how to extend the OpenTelemetry Collector with a custom processor written in Go, and validates it using a Python client.

It simulates
- custom processor
- persistent sending queue
- unstable collector

## Project Structure

- `myprocessor/`: Contains the source code for the custom OpenTelemetry processor (`simpleprocessor`).
- `builder-config.yaml`: Configuration file for the OpenTelemetry Collector Builder (`ocb`).
- `otelcol-dev/`: The generated Collector distribution (code and binary).
- `client-app/`: A Python application that generates OTLP metrics to test the collector.
- `ocb`: The OpenTelemetry Collector Builder binary.

## Prerequisites

- Go 1.24+
- Python 3.10+
- `uv` (Python package manager)

## Getting Started

### 1. Build the Collector

The collector is built using the OpenTelemetry Collector Builder [ocb](https://github.com/open-telemetry/opentelemetry-collector/tree/main/cmd/builder).

```bash
# Already did this for you.
./ocb --config builder-config.yaml
```

This will create a custom collector binary at `otelcol-dev/otelcol-dev`.

### 2. Run the Collector

Start the collector with the provided configuration:

```bash
cd otelcol-dev
go build -o build/collector
./build/collector --config config.yaml
```

The collector is configured to:
- Receive OTLP metrics on ports 4317 (gRPC) and 4318 (HTTP).
- Process metrics using the custom `simple` processor (which logs metric counts).
- Export metrics to the console (`debug` exporter).

### 3. Run the Client

In a separate terminal, run the Python client to generate metrics:

```bash
cd client-app
uv run main.py
```

The client sends a batch of metrics to the collector.


## Custom Processor Implementation

The custom processor is located in `myprocessor/`. It implements the `processor.Metrics` interface and simply logs metrics and potentially mutate values.
