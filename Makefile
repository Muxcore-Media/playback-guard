.PHONY: proto test build

proto:
	PATH="$(HOME)/go/bin:$$PATH" protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/guardv1/guard.proto

test:
	go test ./...

build:
	go build -o bin/playback-guard ./cmd/module
