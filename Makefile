PROTOC_GEN_GO ?= $(HOME)/.local/bin/protoc-gen-go
BUILD_DIR ?= build
PREFIX ?= /usr/local
BIN_DIR ?= $(PREFIX)/bin
ZSH_COMPLETION_DIR ?= $(PREFIX)/share/zsh/site-functions
SYSTEMD_SYSTEM_DIR ?= /etc/systemd/system

PB_BIN := $(BUILD_DIR)/pb
RELAY_BIN := $(BUILD_DIR)/pocket-bridge-relay
AGENTD_BIN := $(BUILD_DIR)/pocket-bridge-agentd
PB_COMPLETION := $(BUILD_DIR)/_pb
BUILD_STAMP := $(BUILD_DIR)/.dir

.PHONY: proto build tidy clean completion install install-completion install-systemd

proto:
	PATH="$(HOME)/.local/bin:$$PATH" protoc \
		--proto_path=. \
		--go_out=. \
		--go_opt=module=github.com/cagedbird043/pocket-bridge \
		proto/bridge.proto

$(BUILD_STAMP):
	mkdir -p $(BUILD_DIR)
	touch $(BUILD_STAMP)

$(PB_BIN): | $(BUILD_STAMP)
	go build -o $(PB_BIN) ./cmd/pb

$(RELAY_BIN): | $(BUILD_STAMP)
	go build -o $(RELAY_BIN) ./cmd/relay

$(AGENTD_BIN): | $(BUILD_STAMP)
	go build -o $(AGENTD_BIN) ./cmd/agentd

build: $(PB_BIN) $(RELAY_BIN) $(AGENTD_BIN)

$(PB_COMPLETION): $(PB_BIN)
	$(PB_BIN) completion zsh > $(PB_COMPLETION)

completion: $(PB_COMPLETION)

install: build
	install -d $(DESTDIR)$(BIN_DIR)
	install -m 0755 $(PB_BIN) $(DESTDIR)$(BIN_DIR)/pb
	install -m 0755 $(RELAY_BIN) $(DESTDIR)$(BIN_DIR)/pocket-bridge-relay
	install -m 0755 $(AGENTD_BIN) $(DESTDIR)$(BIN_DIR)/pocket-bridge-agentd

install-completion: $(PB_COMPLETION)
	install -d $(DESTDIR)$(ZSH_COMPLETION_DIR)
	install -m 0644 $(PB_COMPLETION) $(DESTDIR)$(ZSH_COMPLETION_DIR)/_pb

install-systemd:
	install -d $(DESTDIR)$(SYSTEMD_SYSTEM_DIR)
	install -m 0644 packaging/systemd/pocket-bridge-relay@.service $(DESTDIR)$(SYSTEMD_SYSTEM_DIR)/pocket-bridge-relay@.service
	install -m 0644 packaging/systemd/pocket-bridge-agentd@.service $(DESTDIR)$(SYSTEMD_SYSTEM_DIR)/pocket-bridge-agentd@.service

tidy:
	go mod tidy

clean:
	rm -rf $(BUILD_DIR)
