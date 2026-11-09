# Darkman source review

Reviewed on 2026-11-06 using the official manuals and GitLab source. The main
branch API identified commit `9e54e580651a64832eaf1bda5e4f9e1e802760d1`;
hook behavior was also checked against the commit-pinned `scripts.go`.
Source was read through the web reader because the development environment's
HTTP proxy was unavailable for cloning. Upstream was not built or run here.

## Architecture and the lesson for WSL

Darkman has a mode service with independent listeners. Its location/sun scheduler
is one input; scripts, cached state, IPC, and the portal are outputs. New listeners
receive the initial mode, and repeated requests for the same mode do not generate
a transition. With neither configured coordinates nor geoclue enabled, the
scheduler is not started. This already makes external control a supported design.
Sources: [service.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/service.go),
[scheduler.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/scheduler.go).

For this project, Windows should supply the mode. Darkman's Linux integrations
can remain useful. The new optional `--backend darkman` sends host changes through
the supported `darkman set` command. The standalone backend remains available
when a dependency-free Linux implementation is preferable. Backend failures retry
using the existing watcher reconnection path. No location or solar scheduling is
introduced into the WSL tool.

## Hook compatibility

The original implementation covered only legacy mode-specific directories.
Current Darkman also supports unified `darkman/` hooks, passing the new mode as
one argument. Lookup makes two passes: legacy first and unified second. Within
each pass, system roots are processed in configured order, then the user root.
Later same-named entries replace earlier ones. A nonexecutable entry masks the
earlier hook. Unified hooks override legacy hooks across roots.

This behavior is now implemented and tested. Directory read errors are logged
without discarding other directories. One deliberate difference remains:
standalone hooks execute sequentially in filename order with bounded runtimes.
Current upstream runs scripts concurrently within a transition and uses a mutex
to keep transition script groups from overlapping. The Darkman backend delegates
all hook execution to upstream.

Sources: [pinned scripts.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/9e54e580651a64832eaf1bda5e4f9e1e802760d1/scripts.go),
[xdg.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/xdg.go),
[hook manual](https://darkman.whynothugo.nl/darkman.1/#Custom_executables).

## IPC and the desktop portal

The current socket protocol has newline-delimited `get`, `set`, `toggle`, and
`watch` commands. Watching reports the initial value immediately. The CLI and
`libdarkman` use this socket; the older custom D-Bus API remains in source, but
the changelog deprecates it. Calling the CLI avoids hardcoding a socket path or
reimplementing its configuration discovery. `set` writes the request without
waiting for hook completion.
Sources: [IPC manual](https://darkman.whynothugo.nl/darkman-ipc.5/),
[socket.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/socket.go),
[libdarkman/lib.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/libdarkman/lib.go),
[CLI](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/cmd/darkman/main.go),
[changelog](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/CHANGELOG.md).

The XDG settings backend exposes `org.freedesktop.appearance/color-scheme` and
emits `SettingChanged`. Its values are 0 for no preference, 1 for dark, and 2 for
light. This is a separate application integration from setting a GNOME preference.
The portal frontend must be configured to select Darkman's Settings backend.
Standalone wsl-darkman does not provide that interface; the optional backend lets
the actual Darkman service provide it.
Sources: [portal.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/portal.go),
[portal manual](https://darkman.whynothugo.nl/darkman.1/#XDG_Settings_portal).

## Startup, configuration, and suspend

Darkman can restore the last mode from cache when location is unavailable. It
registers listeners before starting scheduling and supports readiness signaling.
Its supplied systemd unit uses D-Bus readiness. Its solar timer uses CLOCK_BOOTTIME
to account for suspend. For Windows-driven mode, the WSL tool waits for the host's
initial value and resynchronizes on watcher reconnect; it does not assume cached
Linux mode is the current Windows mode. Native suspend/resume behavior still
needs a WSL smoke test.
Sources: [service.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/service.go),
[systemd unit](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/contrib/darkman.service),
[boottimer.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/boottimer/boottimer.go).

Configuration uses XDG search locations and environment overrides. Unknown YAML
fields are rejected. Both location coordinates and geoclue must be absent/disabled
for external control without solar scheduling. The README now explains this and
the optional service dependency. Existing user configuration is not modified.
Sources: [configuration.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/configuration.go),
[configuration tests](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/configuration_test.go),
[configuration manual](https://darkman.whynothugo.nl/darkman.conf.5/).

Also reviewed upstream's installation rules, GTK and Neovim example hooks, and
[ISC licence](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/LICENCE).
No upstream Go code was copied into this implementation. The original Python
prototype has since been replaced with a Go implementation using only the
standard library. The Windows notification script is embedded into the binary.

## Verification

The Go suite covers real unified/legacy hook execution, cross-root overrides,
masks, backend delegation, failed-backend recovery, a simulated Darkman executable,
watcher reconnects, Unix interop socket discovery, timeouts, and process shutdown.
Windows registry events and the real Darkman/portal integration were not executed
in this Linux environment.
