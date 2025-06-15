# CGO_ENABLED=0 <-- automatically added when cross compiling
GOVARS = GOOS=linux GOARCH=amd64
REPO   ?= jpedrob
NAME   ?= test
TAG    ?= v0.0.1

.PHONY: help
help: ### Shows this help
	@grep -E '^[0-9a-zA-Z_-]+[.0-9a-zA-Z_-]*:' $(MAKEFILE_LIST) \
	| awk 'BEGIN {FS = ":.*?### "}; {printf "\033[32;1m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: image
image:
	@echo "==> Building the image $(REPO)/$(NAME):$(TAG)"
	podman build . -t $(REPO)/$(NAME):$(TAG)
	podman push $(REPO)/$(NAME):$(TAG)

.PHONY: deploy
deploy: image ### What
	@echo "==> Deploying $(REPO)/$(NAME):$(TAG)"
	TAG=$(TAG) envsubst < ../test.yaml | kubectl apply -f -
