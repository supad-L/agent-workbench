PYTHON ?= python3
RUNTIME ?= docker
SCRATCH_DIR ?=

.PHONY: test vet build doctor protocol network sandbox verify lint
test:
	$(PYTHON) scripts/dev.py test
vet:
	$(PYTHON) scripts/dev.py vet
build:
	$(PYTHON) scripts/dev.py build
doctor:
	$(PYTHON) scripts/dev.py doctor
protocol:
	$(PYTHON) scripts/dev.py protocol
network:
	$(PYTHON) scripts/dev.py network
sandbox:
	$(PYTHON) scripts/dev.py sandbox --image "$(IMAGE)" --runtime "$(RUNTIME)" --scratch-dir "$(SCRATCH_DIR)"
lint:
	$(PYTHON) scripts/dev.py lint
verify:
	$(PYTHON) scripts/dev.py verify
