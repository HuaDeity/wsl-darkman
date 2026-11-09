# wsl-darkman

Development status: automated tests pass with a simulated Windows host. Native
Windows registry notifications and real WSL/desktop portal integration still
need smoke testing on a Windows machine.

A small, event-driven WSL appearance synchronizer. It follows Windows instead
of sunrise/sunset and uses only Python's standard library on Linux. One persistent
Windows PowerShell process waits on native `RegNotifyChangeKeyValue` notifications;
there is no periodic registry polling or recurring PowerShell startup.

The standalone backend applies preferences and hooks directly. The optional
`--backend darkman` backend supplies Windows mode changes to an installed Darkman,
which provides its existing hooks, IPC, and desktop portal integration.

## Requirements

- WSL with Windows executable interop enabled and Python 3.9 or newer.
- Windows 10/11 and built-in Windows PowerShell 5.1 with `Add-Type` permitted.
- Optional: `gsettings` and a working user D-Bus session for GNOME/GTK preferences.
- Optional: user systemd for background startup. Foreground operation works without it.

## Install and run

From this directory, inside WSL:

```sh
make install
~/.local/bin/wsl-darkman get      # print Windows mode: light or dark
~/.local/bin/wsl-darkman once     # synchronize now
~/.local/bin/wsl-darkman run      # keep following changes; Ctrl-C stops
```

For automatic startup with user systemd:

```sh
systemctl --user daemon-reload
systemctl --user enable --now wsl-darkman.service
journalctl --user -u wsl-darkman.service -f
```

Run these as your regular WSL user. The supplied unit expects the default
`~/.local/bin` installation; change `ExecStart` if you install elsewhere.
The service runs while the WSL user session is active. It does not start the
distribution from Windows or keep WSL running after shutdown. Do not run another
theme controller against the same settings in standalone mode. When using the
Darkman backend, disable Darkman's solar scheduler as described below.

## Behavior

The default source is the Windows **app** setting,
`HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize\AppsUseLightTheme`.
`0` means dark, `1` means light; an absent value follows Windows' light default.
Use `--source system` to follow `SystemUsesLightTheme`, which controls the shell
and taskbar. Windows' Custom mode can set these independently.

The daemon applies the current mode immediately on startup and on reconnect,
then only applies changed modes. Unrelated Personalize value changes and duplicate
notifications do not rerun hooks. Rapid changes may coalesce into the latest mode.
The watcher arms the notification before reading, including after each event.
Failures reconnect with a 1–30 second backoff. A singleton lock prevents duplicate
daemons within the same user runtime/cache directory.

On each mode change:

1. If available, `gsettings` sets `org.gnome.desktop.interface color-scheme`
   to `prefer-dark` or `prefer-light`.
2. Executable hooks in `darkman/` receive `light` or `dark` as their first argument.
   Legacy `light-mode.d` or `dark-mode.d` hooks also work, without arguments.

Hook lookup follows the current Darkman implementation: scan legacy directories
first, then unified `darkman/` directories. Each pass visits `$XDG_DATA_DIRS`
(default `/usr/local/share:/usr/share`) in its configured order, followed by
`$XDG_DATA_HOME` (default `~/.local/share`). Later entries replace earlier entries
with the same filename; a nonexecutable entry removes the earlier hook. Unified
entries override same-named legacy entries even across different data roots.
Each hook needs a shebang and executable permission. Unlike current Darkman,
standalone execution is sequential in filename order, with a timeout per hook.
`WSL_DARKMAN_MODE` is set to `light` or `dark`. Hook output goes to stderr/the journal.
Each action has a 10 second timeout; failures are logged and remaining hooks run.
Failed actions are retried at the next mode change/reconnect or with `once`.
`once` returns nonzero if an action fails.

Example for older GTK applications using named themes:

```sh
mkdir -p ~/.local/share/darkman
cat > ~/.local/share/darkman/gtk-theme <<'EOF'
#!/bin/sh
case "$1" in
    dark) theme=Adwaita-dark ;;
    light) theme=Adwaita ;;
    *) exit 1 ;;
esac
exec gsettings set org.gnome.desktop.interface gtk-theme "$theme"
EOF
chmod +x ~/.local/share/darkman/gtk-theme
```

The named themes must be installed. WSLg applications vary in how they consume
appearance: some require application-specific hooks or a restart. Qt apps and
terminals are not automatically configured by the GTK preference. This utility
does not implement Darkman's socket/D-Bus APIs or a desktop portal itself.

## Using Darkman for application integrations

This mode lets Darkman handle its own hooks and portal while wsl-darkman supplies
the Windows appearance. First install Darkman separately. In the existing
`~/.config/darkman/config.yaml`, set `usegeoclue: false`, remove `lat` and `lng`,
and keep `portal: true` if using its portal. Remove `DARKMAN_LAT`, `DARKMAN_LNG`,
and conflicting `DARKMAN_USEGEOCLUE` environment overrides from Darkman's service
environment. Both configured coordinates and geoclue must be disabled to prevent
solar transitions. Restart Darkman after changing its configuration.

```sh
systemctl --user enable --now darkman.service
wsl-darkman run --backend darkman
```

In this mode each host transition sends `darkman set light` or `darkman set dark`.
wsl-darkman does not also run local hooks or gsettings. Darkman owns their
execution, timing, duplicate suppression, and error reporting; a successful
`darkman set` only confirms that its CLI accepted/sent the request, not that all
application changes finished. A failed CLI call triggers reconnect/backoff and
resends the current host mode, even without another Windows transition.
`wsl-darkman get` still queries Windows; use `darkman get` to query Darkman.

For background startup, use `systemctl --user edit wsl-darkman.service`:

```ini
[Unit]
Requires=darkman.service
After=darkman.service

[Service]
ExecStart=
ExecStart=%h/.local/bin/wsl-darkman run --backend darkman
```

Then reload and restart wsl-darkman. Follow
[Darkman's portal instructions](https://darkman.whynothugo.nl/darkman.1/#XDG_Settings_portal)
to select its settings backend in the desktop portal configuration. Running
Darkman alone does not configure the portal frontend to use it. This integration
has been checked with a simulated CLI; installed-Darkman/portal behavior still
needs validation on the target WSL session.

## Options and troubleshooting

```sh
wsl-darkman run --source system
wsl-darkman run --no-gsettings
wsl-darkman run --hook-timeout 20
wsl-darkman get --powershell /mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe
```

`--no-gsettings` uses hooks only, useful when no GNOME schema/session is present.
For service options, run `systemctl --user edit wsl-darkman.service` and use:

```ini
[Service]
ExecStart=
ExecStart=%h/.local/bin/wsl-darkman run --source system --no-gsettings
```

Then `systemctl --user daemon-reload` and `systemctl --user restart wsl-darkman`.

If interop fails, first check that `powershell.exe -NoProfile -Command '$PSVersionTable.PSVersion'`
works from a WSL shell. The daemon uses the inherited `WSL_INTEROP`, or discovers
the newest accessible interop socket owned by your user in `/run/WSL`. It also
checks the standard `/mnt/c` PowerShell path when systemd lacks Windows entries
in `PATH`. Custom Windows mount locations require `--powershell`.
If notifications stop because the key is deleted or the watcher exits, it is
restarted and the current mode is reapplied. Closing the parent's stdin signals
the Windows watcher to exit, including after unexpected Linux parent termination.

To stop automatic synchronization:

```sh
systemctl --user disable --now wsl-darkman.service
```

## Validation

```sh
make test
```

Tests exercise mode parsing, real legacy and unified hook execution, masking and
precedence, hook failure/timeout isolation, backend delegation/failure handling,
event deduplication, watcher recovery, and graceful shutdown using
a simulated host process. The native Windows registry path needs a WSL smoke test:

1. Start `wsl-darkman run` in WSL and confirm the initial mode is applied.
2. In Windows Settings → Personalization → Colors, change the app mode both ways.
3. Confirm each actual transition produces one application log entry and runs the
   corresponding hooks. In Custom mode, check `--source apps` versus `system`.
4. Stop with Ctrl-C or systemd; verify its Windows PowerShell watcher exits.
5. Restart WSL and confirm the user service reconnects and reapplies the host mode.

See `DARKMAN-STUDY.md` for the upstream source review and design decisions.
