PROTOC_GEN_GO ?= $(HOME)/.local/bin/protoc-gen-go

.PHONY: proto build tidy

proto:
	PATH="$(HOME)/.local/bin:$$PATH" protoc \
		--proto_path=. \
		--go_out=. \
		--go_opt=module=github.com/cagedbird043/pocket-bridge \
		proto/bridge.proto

build:
	go build ./...

tidy:
	go mod tidy
