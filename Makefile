.PHONY: help run test vet build build-image docker-run push-image clean

# --- configuration (override on the command line) ---
LISTEN_ADDR ?= 0.0.0.0:8080
PORT        ?= 8080

IMAGE     ?= cna-dispatcher
TAG       ?= $(shell cat VERSION)
REGISTRY  ?=
IMAGE_REF := $(if $(REGISTRY),$(REGISTRY)/,)$(IMAGE):$(TAG)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

run: ## Run the dispatcher locally (needs a reachable Docker socket + upstreams)
	go run ./cmd/dispatcher

test: ## Run the test suite
	go test ./...

vet: ## Run go vet
	go vet ./...

build: ## Build the local binary into ./bin
	go build -o bin/dispatcher ./cmd/dispatcher

build-image: ## Build the Docker image ($(IMAGE_REF))
	docker build -t $(IMAGE_REF) .

docker-run: build-image ## Build then run the image, publishing the port
	docker run --rm -p $(PORT):8080 -v /var/run/docker.sock:/var/run/docker.sock $(IMAGE_REF)

push-image: build-image ## Build and push the image to the registry (set REGISTRY)
	@if [ -z "$(REGISTRY)" ]; then \
		echo "REGISTRY is not set. Example: make push-image REGISTRY=ghcr.io/cloud-native-airlines TAG=v0.1.0"; \
		exit 1; \
	fi
	docker push $(IMAGE_REF)

clean: ## Remove build artifacts
	rm -rf bin

.DEFAULT_GOAL := help
