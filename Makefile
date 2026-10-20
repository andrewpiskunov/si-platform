-include .env
export

.PHONY: test integration race vet build migrate run demo generate
test:
	go test ./...
integration:
	@test -n "$(TEST_DATABASE_URL)" || (echo "Set TEST_DATABASE_URL to a disposable PostgreSQL database"; exit 1)
	go test ./...
race:
	@test -n "$(TEST_DATABASE_URL)" || (echo "Set TEST_DATABASE_URL to a disposable PostgreSQL database"; exit 1)
	go test -race ./...
vet:
	go vet ./...
build:
	go build -o bin/server ./cmd/server
	go build -o bin/migrate ./cmd/migrate
	go build -o bin/demo ./cmd/demo
	go build -o bin/siq ./cmd/siq
	go build -o bin/admin ./cmd/admin
migrate:
	go run ./cmd/migrate
run:
	go run ./cmd/server
demo:
	go run ./cmd/demo
generate:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

.PHONY: web-check web-build web-dev browser-test
web-check:
	cd web && npm run check && npm test
web-build:
	cd web && npm run build
web-dev:
	cd web && npm run dev

browser-test:
	cd e2e && npm test
