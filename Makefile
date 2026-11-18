GO ?= go
DSN ?= shop:shop@tcp(127.0.0.1:3306)/shop

.PHONY: check test study readme bench heldout serve console

check: ## format, vet, tests, README in step with the reports (about 40 s)
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	$(GO) vet ./...
	$(GO) test -race -p 1 ./...
	$(GO) run ./cmd/shop readme && git diff --exit-code README.md docs/dashboard.svg

test:
	$(GO) test -race -p 1 ./...

study:
	$(GO) run ./cmd/shop study --world dev

readme:
	$(GO) run ./cmd/shop readme

bench:
	$(GO) run ./cmd/shop bench -dsn '$(DSN)'

heldout: ## once, at the end; writes reports/heldout.lock
	$(GO) run ./cmd/shop study --world heldout

serve:
	$(GO) run ./cmd/shop serve -addr :8080

console:
	cd web && npm ci && npm run build
