REPO ?= myrepo/tls-demo

.PHONY: build-full build-stripped push install use-full use-stripped uninstall

build-full:
	docker build --target full -t $(REPO):full .

build-stripped:
	docker build --target stripped -t $(REPO):stripped .

push:
	docker push $(REPO):full
	docker push $(REPO):stripped

install:
	helm upgrade --install tls-demo ./chart/tls-demo --set image.repository=$(REPO) --set image.variant=full

use-full:
	helm upgrade tls-demo ./chart/tls-demo --set image.repository=$(REPO) --set image.variant=full

use-stripped:
	helm upgrade tls-demo ./chart/tls-demo --set image.repository=$(REPO) --set image.variant=stripped

uninstall:
	helm uninstall tls-demo || true
