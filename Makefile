EXECUTABLES := kubectl docker
K := $(foreach exec,$(EXECUTABLES),\
	$(if $(shell which $(exec)),some string,$(error "Error: command $(exec) not found in PATH - this is required for makefile to proceed")))

ROOT := $(dir $(realpath $(lastword $(MAKEFILE_LIST))))

.PHONY: build deploy undeploy push

build:
	docker build -t paulmw/minibench:0.0.1 ${ROOT} --load

push: build
	docker push paulmw/minibench:0.0.1

deploy:
	kubectl apply -f ${ROOT}/manifest.yaml

undeploy:
	kubectl delete -f ${ROOT}/manifest.yaml