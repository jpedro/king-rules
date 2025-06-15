# CGO_ENABLED=0 <-- automatically added when cross compiling
GOVARS = GOOS=linux GOARCH=amd64
REPO   ?= docker.io/jpedrob
NAME   ?= king-rules
TAG    ?= v1.0.1
NUMBER ?= 01

.PHONY: test
test:
	@echo "==> Building and running locally"
	go build -o /tmp/$(NAME).e
	@NAMESPACE=$(shell kubectl config view -o=jsonpath="{.contexts[?(@.name=='$(shell kubectl config current-context)')].context.namespace}") \
		LOG_LEVEL=debug /tmp/$(NAME).e --kube-config ~/.kube/config

.PHONY: build
build:
	@echo "==> Building for linux // amd64"
	$(GOVARS) go build -o /tmp/$(NAME).e

.PHONY: image
image: build
	@echo "==> Building the image $(REPO)/$(NAME):$(TAG)"
	podman build . -t $(NAME)
	podman tag $(NAME) $(REPO)/$(NAME):$(TAG)
	podman push $(REPO)/$(NAME):$(TAG)

.PHONY: deploy
deploy: # docker
	@echo "==> Deploying king-rules $(TAG)"
	TAG=$(TAG) envsubst < k8s/deployment.yaml | kubectl apply -f -

.PHONY: number
number:
	@echo "==> Deploying echo number $(NUMBER)"
	NUMBER=$(NUMBER) envsubst < example/echo.yaml | kubectl apply -f -

# .PHONY: okteto
# okteto:
# 	@echo "==> Deploying echo number $(NUMBER)"
# 	NUMBER=$(NUMBER) envsubst < example/okteto.yaml | kubectl apply -f -
