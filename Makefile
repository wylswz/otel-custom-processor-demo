collector:
	cd otelcol-dev && go build -o ./build/otelcol-dev

run-collector: collector
	cd otelcol-dev && ./build/otelcol-dev --config config.yaml

run-client:
	cd client-app && uv run main.py

.PHONY: docker
docker:
	cd docker && docker-compose up