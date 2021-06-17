# CGO_ENABLED=0 <-- automatically added when cross compiling
GOVARS = GOOS=linux GOARCH=amd64
REPO   ?= jpedrob
NAME   ?= king-rules
TAG    ?= latest
NUMBER ?= 01

.PHONY: test
test:
	@echo "==> Building and running locally"
	go build -o $(NAME)
	@NAMESPACE=$(shell kubectl config view -o=jsonpath="{.contexts[?(@.name=='$(shell kubectl config current-context)')].context.namespace}") \
		LOG_LEVEL=debug ./$(NAME) --kube-config ~/.kube/config

.PHONY: build
build:
	@echo "==> Building for linux/amd64"
	$(GOVARS) go build -o $(NAME)

.PHONY: docker
docker: build
	@echo "==> Building the docker image $(REPO)/$(NAME):$(TAG)"
	docker build . -t $(NAME)
	docker tag $(NAME) $(REPO)/$(NAME)
	docker tag $(NAME) $(REPO)/$(NAME):$(TAG)
	docker push $(REPO)/$(NAME)
	docker push $(REPO)/$(NAME):$(TAG)

.PHONY: deploy
deploy: # docker
	@echo "==> Deploying king-rules $(TAG)"
	TAG=$(TAG) envsubst < k8s/rbac.yaml       | kubectl apply -f -
	TAG=$(TAG) envsubst < k8s/deployment.yaml | kubectl apply -f -

.PHONY: number
number:
	@echo "==> Deploying echo number $(NUMBER)"
	NUMBER=$(NUMBER) envsubst < example/echo.yaml | kubectl apply -f -

.PHONY: okteto
okteto:
	@echo "==> Deploying echo number $(NUMBER)"
	NUMBER=$(NUMBER) envsubst < example/okteto.yaml | kubectl apply -f -
