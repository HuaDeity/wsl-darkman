# Linux application appearance

Windows supplies a light/dark preference. Linux applications receive that
preference through different toolkit and desktop interfaces. This service
implements the common interfaces directly and keeps hooks for the rest.

## Modern applications: the XDG settings portal

The standard key is `org.freedesktop.appearance` / `color-scheme`, a D-Bus `uint32`:

| Value | Meaning |
| --- | --- |
| 0 | No preference, including startup before Windows mode is known |
| 1 | Prefer dark appearance |
| 2 | Prefer light appearance |

Our implementation exports `Read`, `ReadAll`, the lowercase read-only `version`
property, and `SettingChanged`. `ReadAll` supports namespace filters and trailing
wildcards. Unknown single keys return `org.freedesktop.portal.Error.NotFound`.
It never accepts mode changes from Linux applications.

The desktop portal frontend forwards these settings to applications. Configure
its Settings implementation to prefer `wsl-darkman`, as shown in the README.
Running only our implementation without selecting it in the frontend is not
sufficient. A normal user session bus is required; no separate Darkman daemon is
used. The service restarts on bus disconnection rather than silently leaving a
stale appearance provider.

Sources: [settings interface specification](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.impl.portal.Settings.html),
[frontend settings implementation](https://github.com/flatpak/xdg-desktop-portal/blob/main/desktop-portal/settings.c),
[portal discovery](https://github.com/flatpak/xdg-desktop-portal/blob/main/desktop-portal/xdp-portal-config.c).

Libadwaita applications can follow the system preference, but can also force a
light or dark appearance. Their own override wins. Portal support does not imply
that every GTK4 application consumes the preference or has a dark style.
Source: [Libadwaita styles and appearance](https://gnome.pages.gitlab.gnome.org/libadwaita/doc/main/styles-and-appearance.html).

## GNOME preferences and older GTK themes

The direct preference is:

```sh
gsettings set org.gnome.desktop.interface color-scheme prefer-dark
gsettings set org.gnome.desktop.interface color-scheme prefer-light
```

We probe the installed schema first. A minimal WSL distribution without these
keys can still use the portal and custom hooks. The key is a preference, not a
named theme. Older GTK applications may instead follow:

```sh
gsettings set org.gnome.desktop.interface gtk-theme Adwaita-dark
gsettings set org.gnome.desktop.interface gtk-theme Adwaita
```

Use `--gtk-dark-theme` and `--gtk-light-theme` to request an installed pair. The
service does not impose a default theme or change fonts, icon packs, or wallpaper.
On a full desktop, XSettings integration may propagate the theme to running GTK3
apps. WSLg alone does not guarantee that propagation; an app may need a restart
or an appropriate settings daemon. Apps using custom styles may ignore it.

We do not set `GTK_THEME` globally: changing the daemon's environment cannot
change an already-running application's environment. We also do not rewrite
`gtk-3.0/settings.ini`, which would be a persistent fallback rather than a
universal live update mechanism.

Sources: [GNOME interface schema](https://github.com/GNOME/gsettings-desktop-schemas/blob/master/schemas/org.gnome.desktop.interface.gschema.xml.in),
[Darkman's GTK3 example](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/examples/gtk3-theme.sh).

## KDE, Qt, Xfce, and application-specific integrations

Qt's effective color scheme is supplied by its platform integration and can be
overridden by the application. A GNOME preference does not configure every Qt
palette or Qt5 theme plugin. KDE's theme tools and per-application configuration
remain separate mechanisms. Darkman's KDE example uses `lookandfeeltool` to apply
a global Plasma theme; that can change more than the light/dark preference, so
we do not invoke it automatically.

Sources: [Qt color scheme behavior](https://doc.qt.io/qt-6/qstylehints.html#colorScheme-prop),
[Darkman's Plasma example](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/examples/kde-global-theme.sh).

The included optional hooks show two narrower integrations:

| Application/session | Command used |
| --- | --- |
| Xfce | `xfconf-query -c xsettings -p /Net/ThemeName -s THEME` |
| Neovim with neovim-remote | Send `set background=dark` or `set background=light` to existing servers |

Install only hooks matching software you use:

```sh
mkdir -p ~/.local/share/darkman
install -m755 examples/hooks/xfce-theme ~/.local/share/darkman/xfce-theme
# Or, with neovim-remote installed:
install -m755 examples/hooks/neovim-background ~/.local/share/darkman/neovim-background
```

The Xfce example uses Adwaita/Adwaita-dark; edit the names for your installed
themes. Neovim must be listening for remote commands, and its colorscheme must
respect the `background` option. Terminals, editors, and applications with their
own theme systems need similarly specific hooks. None of these example hooks
are enabled by installation of the service itself.

Sources: [Darkman's Xfce example](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/examples/xfce-theme.sh),
[Darkman's Neovim example](https://gitlab.com/WhyNotHugo/darkman/-/blob/main/examples/neovim-background.sh).

## Hook lookup and behavior

Hooks are executables with a shebang and executable permission. Lookup makes two
passes: legacy `dark-mode.d`/`light-mode.d`, then unified `darkman/`. Each pass
visits `$XDG_DATA_DIRS` in configured order (default `/usr/local/share:/usr/share`),
then `$XDG_DATA_HOME` (default `~/.local/share`). Later same-named entries replace
earlier entries; nonexecutable entries mask earlier hooks. Unified entries take
precedence over legacy entries across roots.

Unified hooks receive the new mode as `$1`; legacy hooks have no arguments.
All hooks receive `WSL_DARKMAN_MODE`. They run sequentially in filename order,
with output in the journal. A timed-out hook and its child processes are stopped;
remaining hooks continue. Service shutdown cancels the current action and stops
starting new actions.
