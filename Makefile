PREFIX ?= $(HOME)/.local
GO ?= go

.PHONY: build install test vet clean
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o bin/wsl-darkman ./cmd/wsl-darkman

install: build
	install -Dm755 bin/wsl-darkman "$(DESTDIR)$(PREFIX)/bin/wsl-darkman"
	install -Dm644 contrib/systemd/wsl-darkman.service "$(DESTDIR)$(PREFIX)/lib/systemd/user/wsl-darkman.service"
	install -Dm644 contrib/portal/wsl-darkman.portal "$(DESTDIR)$(PREFIX)/share/xdg-desktop-portal/portals/wsl-darkman.portal"
	install -d "$(DESTDIR)$(PREFIX)/share/dbus-1/services"
	sed 's|@BINDIR@|$(PREFIX)/bin|g' contrib/dbus/org.freedesktop.impl.portal.desktop.wsl_darkman.service.in > "$(DESTDIR)$(PREFIX)/share/dbus-1/services/org.freedesktop.impl.portal.desktop.wsl_darkman.service"

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf bin
