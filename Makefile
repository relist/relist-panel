.PHONY: build build-web test typecheck

build: build-web
	go build -o relist-panel .

build-web:
	go generate ./internal/webassets/...

test: build-web
	go test ./...

typecheck:
	pnpm --dir web typecheck
