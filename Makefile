collector:
	cd otelcol-dev && go build -o ./build/otelcol-dev

run-collector: collector
	cd otelcol-dev && ./build/otelcol-dev --config jsonnet://config.jsonnet

run-client:
	cd client-app && uv run main.py

.PHONY: docker
docker:
	cd docker && docker-compose up

.PHONY: install-ocb
install-ocb:
	curl --proto '=https' --tlsv1.2 -fL -o ocb https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/cmd%2Fbuilder%2Fv0.140.0/ocb_0.140.0_darwin_arm64 && chmod +x ocb