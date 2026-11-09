PREFIX ?= $(HOME)/.local

.PHONY: install test
install:
	install -Dm755 wsl-darkman "$(DESTDIR)$(PREFIX)/bin/wsl-darkman"
	install -Dm644 wsl-darkman.service "$(DESTDIR)$(PREFIX)/lib/systemd/user/wsl-darkman.service"

test:
	python3 -m unittest discover -s tests -v
