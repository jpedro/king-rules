# CGO_ENABLED=0 <-- automatically added when cross compiling
GOVARS = GOOS=linux GOARCH=amd64
REPO   ?= jpedrob
NAME   ?= king-rules
TAG    ?= latest
NUMBER ?= 01

.PHONY: test
test:
	@echo "==> Building locally"
	go build -o $(NAME)
	@echo "==> Executing the local build"
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
	docker tag $(NAME) $(REPO)/$(NAME):$(TAG)
	docker push $(REPO)/$(NAME):$(TAG)

	@echo "==> Pushing the docker image as latest"
	docker tag $(NAME) $(REPO)/$(NAME)
	docker push $(REPO)/$(NAME)

.PHONY: deploy
deploy: docker
	@echo "==> Deploying the docker image"
	envsubst < k8s/deployment.yaml | kubectl apply -f -
	kubectl get -f k8s/deployment.yaml -o yaml

.PHONY: update
update:
	envsubst < k8s/deployment.yaml | kubectl apply -f -
	kubectl get -f k8s/deployment.yaml -o yaml

.PHONY: number
number:
	@echo "==> Deploying number $(NUMBER)"
	NUMBER=$(NUMBER) envsubst < example/deployment.yaml | kubectl apply -f -
	NUMBER=$(NUMBER) envsubst < example/deployment.yaml | kubectl get   -f -
	NUMBER=$(NUMBER) envsubst < example/service.yaml    | kubectl apply -f -
	NUMBER=$(NUMBER) envsubst < example/service.yaml    | kubectl get   -f -
