.PHONY: dev install run run-bridge run-core run-all test lint format clean

# Python core
dev:
	cd core && poetry install --with dev

install:
	cd core && poetry install

run-core:
	cd core && poetry run claude-whatsapp

run-core-debug:
	cd core && poetry run claude-whatsapp --debug

# Go bridge
build-bridge:
	cd bridge && go build -o bin/bridge .

run-bridge:
	cd bridge && go run .

# Both
run-all:
	$(MAKE) run-bridge &
	sleep 3
	$(MAKE) run-core

# Test
test:
	cd core && poetry run pytest tests/ -v --cov=src

# Lint
lint:
	cd core && poetry run black --check src tests
	cd core && poetry run isort --check src tests

format:
	cd core && poetry run black src tests
	cd core && poetry run isort src tests

clean:
	find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true
	find . -type f -name "*.pyc" -delete 2>/dev/null || true
	rm -rf bridge/bin
