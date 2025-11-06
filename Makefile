EXECUTABLES := kubectl docker
K := $(foreach exec,$(EXECUTABLES),\
	$(if $(shell which $(exec)),some string,$(error "Error: command $(exec) not found in PATH - this is required for makefile to proceed")))

ROOT := $(dir $(realpath $(lastword $(MAKEFILE_LIST))))
VERSION := $(shell cat ${ROOT}/VERSION)

.PHONY: build deploy undeploy push bump

build:
	docker build ${ROOT}

push:
	docker build -t paulmw/minibench:${VERSION} ${ROOT} --load
	docker push paulmw/minibench:${VERSION}

bump:
	@echo "Incrementing version from ${VERSION}"
	@echo ${VERSION} | awk -F. '{$$NF = $$NF + 1;} 1' | sed 's/ /./g' > ${ROOT}/VERSION
	@echo "New version: $$(cat ${ROOT}/VERSION)"

deploy:
	@echo "Deploying with version ${VERSION}"
	@sed 's|__VERSION__|${VERSION}|g' ${ROOT}/manifest.yaml | kubectl apply -f -

undeploy:
	kubectl delete -f ${ROOT}/manifest.yaml