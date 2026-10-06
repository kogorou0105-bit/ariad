GO_SOURCES := $(shell find cmd internal tests -type f -name '*.go' -print)
# Keep this list explicit: npm dependencies may contain Go files beneath the
# module root and must never become part of repository Go checks.
GO_PACKAGES := ./cmd/... ./internal/... ./tests/... ./db/...
GOLANGCI_LINT := go tool golangci-lint
GOOSE := go tool goose

.PHONY: check check-frontend check-go format format-check lint test generate migrate-status migrate-up clean

check: check-frontend check-go

check-frontend:
	npm run check:frontend

check-go: format-check lint test

format:
	gofmt -w $(GO_SOURCES)

format-check:
	@unformatted="$$(gofmt -l $(GO_SOURCES))"; \
	if [ -n "$$unformatted" ]; then \
		echo "Go files require formatting:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

lint:
	go vet $(GO_PACKAGES)
	$(GOLANGCI_LINT) run $(GO_PACKAGES)

test:
	go test $(GO_PACKAGES)

migrate-status:
	@test -n "$$ARIAD_DATABASE_URL" || (echo "ARIAD_DATABASE_URL is required"; exit 1)
	$(GOOSE) -dir db/migrations postgres "$$ARIAD_DATABASE_URL" status

migrate-up:
	@test -n "$$ARIAD_DATABASE_URL" || (echo "ARIAD_DATABASE_URL is required"; exit 1)
	$(GOOSE) -dir db/migrations postgres "$$ARIAD_DATABASE_URL" up

generate:
	go tool sqlc generate -f db/sqlc.yaml

clean:
	rm -rf apps/console/dist apps/widget/dist
