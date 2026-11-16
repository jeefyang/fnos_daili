GO ?= go
DIST ?= dist
PLATFORM ?= x86
SAMPLE_APPNAME ?= daili.demo
SAMPLE_TARGET ?= http://192.168.1.10:8123
SAMPLE_VERSION ?= 1.0.0

ifeq ($(PLATFORM),arm)
PROXY_ARCH := arm64
else
PROXY_ARCH := amd64
endif

PROXY_BIN := $(DIST)/proxy-linux-$(PROXY_ARCH)
SAMPLE_OUT := $(DIST)/$(SAMPLE_APPNAME)-$(SAMPLE_VERSION).fpk

.PHONY: all test proxy sample clean

all: test sample

test:
	$(GO) test ./...

proxy: $(PROXY_BIN)

$(PROXY_BIN):
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(PROXY_ARCH) $(GO) build -trimpath -ldflags "-s -w -buildid=" -o $@ ./cmd/proxy

sample: $(PROXY_BIN)
	$(GO) run ./cmd/build-fpk --appname $(SAMPLE_APPNAME) --display-name "示例代理" --target $(SAMPLE_TARGET) --version $(SAMPLE_VERSION) --platform $(PLATFORM) --proxy-bin $(PROXY_BIN) --out $(SAMPLE_OUT)

clean:
	rm -rf $(DIST)
