REPO ?= ghcr.io/taha2samy-3/demo/tls-demo
NAMESPACE ?= tls-demo

.PHONY: build-full build-stripped push install-full install-stripped use-full use-stripped uninstall template-full template-stripped

build-full:
	docker build --target full -t $(REPO):full .

build-stripped:
	docker build --target stripped -t $(REPO):stripped .

push:
	docker push $(REPO):full
	docker push $(REPO):stripped

install-full:
	helm upgrade --install tls-demo ./chart/tls-demo -n $(NAMESPACE) --create-namespace --set image.repository=$(REPO) -f chart/tls-demo/values-full.yaml

install-stripped:
	helm upgrade --install tls-demo ./chart/tls-demo -n $(NAMESPACE) --create-namespace --set image.repository=$(REPO) -f chart/tls-demo/values-stripped.yaml

use-full: install-full

use-stripped: install-stripped

uninstall:
	helm uninstall tls-demo -n $(NAMESPACE) || true

template-full:
	helm template tls-demo ./chart/tls-demo -n $(NAMESPACE) --set image.repository=$(REPO) -f chart/tls-demo/values-full.yaml

template-stripped:
	helm template tls-demo ./chart/tls-demo -n $(NAMESPACE) --set image.repository=$(REPO) -f chart/tls-demo/values-stripped.yaml

