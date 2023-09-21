# override to push to a different registry or tag the image differently
REGISTRY ?= docker.cloudsmith.io/tetrate/tis-containers
TAG ?= v0.5.0

# Make sure we pick up any local overrides.
-include .makerc

build: istio-registry-sync
istio-registry-sync:
	go build -o istio-registry-sync github.com/tetratelabs/istio-registry-sync/cmd/istio-registry-sync
	chmod +x istio-registry-sync

run: istio-registry-sync
	./istio-registry-sync serve --kube-config ~/.kube/config


build-static: docker/istio-registry-sync-static

docker/istio-registry-sync-static:
	CGO_ENABLED=0 GOOS=linux go build \
		-a --ldflags '-extldflags "-static"' -tags netgo -installsuffix netgo \
		-o docker/istio-registry-sync-static github.com/tetratelabs/istio-registry-sync/cmd/istio-registry-sync
	chmod +x docker/istio-registry-sync-static

docker-build: docker/istio-registry-sync-static
	docker build -t $(REGISTRY)/istio-registry-sync:$(TAG) docker/

docker-push: docker-build
	docker push $(REGISTRY)/istio-registry-sync:$(TAG)

docker-run: docker-build
	# local run, mounting kube config into the container and allowing it to use a host network to access the remote cluster
	@docker run \
		-v ~/.kube/config:/etc/istio-registry-sync/kube-config \
		--network host \
		$(REGISTRY)/istio-registry-sync:$(TAG) serve --kube-config /etc/istio-registry-sync/kube-config

clean:
	rm -f istio-registry-sync
