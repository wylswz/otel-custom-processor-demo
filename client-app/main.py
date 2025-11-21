import time
from opentelemetry import metrics
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter
from opentelemetry.sdk.resources import Resource

def main():
    # Define resource with service name
    resource = Resource.create(attributes={
        "service.name": "python-client-app"
    })

    # Configure OTLP exporter (defaults to localhost:4317)
    exporter = OTLPMetricExporter(insecure=True, max_export_batch_size=1)
    
    # Create a metric reader that exports every 5 seconds
    reader = PeriodicExportingMetricReader(exporter, export_interval_millis=5000)
    
    # Create MeterProvider
    provider = MeterProvider(resource=resource, metric_readers=[reader])
    metrics.set_meter_provider(provider)
    
    meter = metrics.get_meter("my.meter")
    
    # Create a counter
    counter = meter.create_counter(
        "work_done",
        description="Counts the amount of work done",
        unit="1",
    )
    
    print("Sending metrics...")
    for i in range(100):
        counter.add(1, {"work.type": "manual", "work.id": str(i)})
        print(f"Emitted metric {i+1}")

    time.sleep(5)
    # Ensure all metrics are sent
    provider.shutdown()
    print("Done.")

if __name__ == "__main__":
    main()
