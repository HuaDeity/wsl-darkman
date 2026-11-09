PREFIX ?= $(HOME)/.local
GO ?= go

.PHONY: build install test vet clean
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o bin/wsl-darkman ./cmd/wsl-darkman

install: build
	install -Dm755 bin/wsl-darkman "$(DESTDIR)$(PREFIX)/bin/wsl-darkman"
	install -Dm644 contrib/systemd/wsl-darkman.service "$(DESTDIR)$(PREFIX)/lib/systemd/user/wsl-darkman.service"

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf bin
