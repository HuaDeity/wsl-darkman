# wsl-darkman

A standalone Go user service that synchronizes Linux application appearance with
Windows. It follows native Windows registry notifications rather than sunrise and
sunset. Darkman is a design reference, not a runtime dependency.

Windows appearance is published through our own XDG settings portal, applied to
GNOME preferences where available, and passed to application hooks. The Windows
watcher is embedded in the Linux binary and uses built-in Windows PowerShell.
There is no periodic registry polling or recurring PowerShell startup.

Development status: tests cover a simulated Windows host, a real private D-Bus
session, and real GSettings/dconf with a reduced schema fixture. Native Windows
notifications and end-to-end WSL application appearance still need smoke testing.

## Requirements

- WSL with Windows executable interop enabled.
- Windows 10/11 with Windows PowerShell 5.1 and `Add-Type` permitted.
- Go 1.22+ to build. The executable needs no Python or separately installed Go runtime.
- A user session D-Bus for the default portal mode. Use `--no-portal` otherwise.
- `xdg-desktop-portal` for applications to consume the portal preference.
- Optional: `gsettings`, GNOME interface schemas, and dconf for direct GNOME/GTK settings.

The Go dependencies are `godbus/dbus` and its `x/sys` dependency, pinned in
`go.mod`/`go.sum` and linked into the binary. Darkman is never invoked.

## Build and install

Inside WSL, from this directory:

```sh
make install
systemctl --user daemon-reload
systemctl --user enable --now wsl-darkman.service
journalctl --user -u wsl-darkman.service -f
```

The default prefix is `~/.local`. Installation includes the executable, user
systemd unit, portal descriptor, and D-Bus activation file. It does not rewrite
application configurations or your portal selection. Follow the portal setup
below to make applications use this service's preference.

`make build` writes `bin/wsl-darkman`. Builds disable cgo and produce a static
Linux executable. Use `make GO=/path/to/go install` for a compiler outside `PATH`.
The supplied unit expects `~/.local/bin`; adjust its `ExecStart` if using a custom
prefix. `DESTDIR` is supported for packaging.

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o bin/wsl-darkman-linux-amd64 ./cmd/wsl-darkman
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o bin/wsl-darkman-linux-arm64 ./cmd/wsl-darkman
```

## Linux application settings

There is no single setting that controls every Linux application. The detailed
[Linux appearance guide](docs/linux-appearance.md) explains the settings and
application-specific hooks learned from Darkman and upstream toolkit documentation.

| Integration | What this service does |
| --- | --- |
| XDG settings portal | Publishes `org.freedesktop.appearance/color-scheme`: `1` dark, `2` light, `0` before Windows mode is known; emits change signals. |
| GNOME color preference | Sets `org.gnome.desktop.interface color-scheme` to `prefer-dark` or `prefer-light` when the installed schema provides it. |
| Named GTK themes | Optionally sets `org.gnome.desktop.interface gtk-theme` using the pair of theme names you supply. |
| Other applications | Runs executable hooks with the new mode; examples cover Xfce and Neovim. |

Theme selection is opt-in so the service does not guess your preferred or
installed themes:

```sh
wsl-darkman run --gtk-light-theme Adwaita --gtk-dark-theme Adwaita-dark
```

Both names must be supplied and installed. Missing optional GNOME schemas are
logged and skipped; failure to apply an explicitly requested theme is reported.
An application configured to force its own appearance may ignore system changes.

## Select our settings portal

Install your distribution's `xdg-desktop-portal`, then merge this entry into the
`[preferred]` section of your existing
`~/.config/xdg-desktop-portal/portals.conf`:

```ini
[preferred]
org.freedesktop.impl.portal.Settings=wsl-darkman
```

Preserve existing defaults and other interface selections. If you already use a
Settings provider such as `gtk`, keep it after ours, for example
`org.freedesktop.impl.portal.Settings=wsl-darkman;gtk`. Our implementation exposes
only `color-scheme`; retained providers can supply other keys. Desktop-specific
configuration files can take precedence; see the [portal configuration manual](https://flatpak.github.io/xdg-desktop-portal/docs/portals.conf.html).

```sh
systemctl --user restart wsl-darkman.service
systemctl --user restart xdg-desktop-portal.service
gdbus call --session --dest org.freedesktop.portal.Desktop \
  --object-path /org/freedesktop/portal/desktop \
  --method org.freedesktop.portal.Settings.ReadOne \
  org.freedesktop.appearance color-scheme
```

The result should contain `uint32 1` or `uint32 2`, matching Windows. The backend
owns `org.freedesktop.impl.portal.desktop.wsl_darkman`. Applications read the
public portal frontend, not that implementation name directly.

Current portal releases discover per-user descriptors under
`~/.local/share/xdg-desktop-portal/portals`. If your distribution's older frontend
only scans its system portal directory, install just the descriptor there:

```sh
sudo install -Dm644 contrib/portal/wsl-darkman.portal /usr/share/xdg-desktop-portal/portals/wsl-darkman.portal
```

The user unit uses D-Bus readiness, so it becomes active when the portal name is
owned. A session bus disconnection stops the watcher and exits with an error;
systemd restarts the service and reconnects it.

## Commands and service options

```sh
wsl-darkman get                  # print current Windows mode
wsl-darkman once                 # apply GNOME preferences and hooks once
wsl-darkman run                  # publish the portal and synchronize continuously
wsl-darkman run --source system  # follow Windows shell/taskbar instead of app mode
wsl-darkman run --no-gsettings   # portal and hooks only
wsl-darkman run --no-portal      # GNOME preferences and hooks only
wsl-darkman run --hook-timeout 20
```

`once` does not provide a persistent portal. The default Windows source is
`AppsUseLightTheme`; `--source system` selects `SystemUsesLightTheme` under
`HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`.
`0` is dark and `1` is light; an absent value uses light.

For persistent options, use `systemctl --user edit wsl-darkman.service`:

```ini
[Service]
ExecStart=
ExecStart=%h/.local/bin/wsl-darkman run --gtk-light-theme Adwaita --gtk-dark-theme Adwaita-dark
```

Then reload and restart the unit. Without a user session bus, use this override
instead, and do not select our portal in `portals.conf`:

```ini
[Service]
Type=simple
BusName=
ExecStart=
ExecStart=%h/.local/bin/wsl-darkman run --no-portal
```

To upgrade, run `make install`, reload systemd, and restart the unit. Remove any
old `--backend` options or Darkman service dependencies from local overrides;
the Darkman backend has been removed. Stop another appearance controller if it
writes the same Linux preferences.

## Hooks and recovery

Hooks use Darkman's executable convention without requiring Darkman: unified
`darkman/` directories receive `light` or `dark` as `$1`; legacy `light-mode.d`
and `dark-mode.d` hooks receive no arguments. `WSL_DARKMAN_MODE` is also set.
Search and masking rules are documented in the [appearance guide](docs/linux-appearance.md).

The host mode is applied at startup and reconnect. Duplicate events are ignored;
rapid host changes may coalesce to the latest mode. Failed host connections retry
with a 1–30 second backoff. Each hook has a 10 second timeout; failures are logged
and remaining hooks run. Failed appearance actions retry at the next mode change,
reconnect, or `once`. `once` returns nonzero for action failures.

If interop fails, first check that `powershell.exe -NoProfile -Command '$PSVersionTable.PSVersion'`
works inside WSL. The service uses an inherited valid `WSL_INTEROP` socket or finds
the newest one owned by your user under `/run/WSL`. It checks the standard `/mnt/c`
PowerShell path when Windows entries are absent from `PATH`. A custom mount
location needs `--powershell /path/to/powershell.exe`.

To disable automatic synchronization:

```sh
systemctl --user disable --now wsl-darkman.service
```

## Validation and layout

```sh
make test
make vet
go test -race ./...
```

Integration tests need `dbus-daemon`; the real settings test also needs
`gsettings`, `glib-compile-schemas`, and `dconf-service`. They use private bus
sessions and temporary settings databases. These integration checks are skipped
when their required development tools are absent. The race detector needs a C
compiler; production builds do not.

```text
cmd/wsl-darkman/     Executable entry point
internal/app/        CLI, synchronization, Linux settings, portal, and tests
contrib/systemd/     User service unit
contrib/dbus/        D-Bus activation template
contrib/portal/      XDG portal descriptor
examples/hooks/      Optional application integrations
docs/                Design notes and application settings reference
bin/                 Generated binaries (ignored by Git)
```

For the remaining WSL smoke test, start the service, change Windows app mode both
ways, check the public portal result and application appearance, and verify that
service stop/restart and WSL suspend/resume clean up and reconnect the Windows
watcher. See the [Darkman study](docs/darkman-study.md) for the upstream findings.
