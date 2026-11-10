# Lessons from Darkman

Reviewed on 2026-11-06 and refined on 2026-11-10. The original source review
identified main commit `9e54e580651a64832eaf1bda5e4f9e1e802760d1`.

## What we reuse as a design

Darkman separates a mode source from listeners for application integrations.
This service follows that separation: Windows registry notifications supply the
mode; our own portal, direct GNOME preferences, and hooks apply it to Linux.
There is no Darkman CLI backend, solar scheduler, or location dependency.
Sources: [service.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/service.go),
[scheduler.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/scheduler.go).

The most useful application lesson is that a preference is different from a
named theme. Darkman exposes the XDG settings portal for supporting applications
and supplies example scripts for GTK3, Plasma, Xfce, terminals, and editors.
Our implementation now exposes the appearance portal itself and makes GTK theme
selection opt-in. Desktop-specific and application-specific actions remain
explicit hooks. See [Linux appearance](linux-appearance.md) for exact settings,
commands, sources, and limitations.
Sources: [portal.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/portal.go),
[example scripts](https://gitlab.com/WhyNotHugo/darkman/-/tree/main/examples).

## Standards implemented directly

The service publishes `org.freedesktop.appearance/color-scheme` through
`org.freedesktop.impl.portal.Settings`, including namespace-filtered reads and
change signals. The implementation follows the XDG specification, including its
lowercase `version` property and defined `uint32` values. It is read-only and does
not implement Darkman's custom control socket or deprecated custom D-Bus API.
Source: [XDG settings specification](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.impl.portal.Settings.html).

The frontend must discover our `.portal` descriptor and select it in
`portals.conf`. Other Settings implementations may be listed after ours to supply
keys we do not export. The user unit advertises D-Bus readiness, and a separate
D-Bus activation descriptor can start it on demand. This is our own service,
using the normal desktop portal infrastructure.
Sources: [portal configuration](https://flatpak.github.io/xdg-desktop-portal/docs/portals.conf.html),
[backend installation](https://flatpak.github.io/xdg-desktop-portal/docs/writing-a-new-backend.html).

## Hook compatibility

Unified `darkman/` hooks receive the new mode as an argument and override
same-named legacy mode-specific hooks. Later roots replace earlier roots, with
user data visited last; nonexecutable entries mask earlier hooks. That executable
convention works independently of the Darkman daemon.

Our hooks run sequentially with per-action timeouts. Current Darkman runs hooks
concurrently within a serialized transition group. The timeout and cancellation
behavior is an intentional local implementation choice.
Sources: [pinned scripts.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/9e54e580651a64832eaf1bda5e4f9e1e802760d1/scripts.go),
[xdg.go](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/xdg.go).

No upstream Go source was copied. The application integrations were implemented
from the documented settings and protocols. Sources were read through the web
reader and public source APIs; upstream Darkman was not built or executed here.

## Validation

Tests exercise private real D-Bus sessions, initial and changed portal values,
namespace filters, duplicate suppression, read-only properties, bus ownership,
Windows-to-portal publication, bus-loss cleanup, hooks, process timeouts, and
interop socket discovery. Real GSettings/dconf reads and writes use a reduced
GNOME schema fixture and temporary configuration directories. These checks do
not prove an application's rendered appearance or Windows interop behavior;
those still need the WSL smoke test described in the README.
