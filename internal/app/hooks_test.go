//go:build linux

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookPrecedenceArgumentsAndIsolation(t *testing.T) {
	root := testEnvironment(t)
	t.Setenv("CALLS", filepath.Join(root, "calls"))
	executable(t, filepath.Join(root, "system/dark-mode.d/10-legacy"), `echo system >> "$CALLS"`)
	executable(t, filepath.Join(root, "user/dark-mode.d/10-legacy"), `printf 'legacy:%s:%s\n' "$#" "$WSL_DARKMAN_MODE" >> "$CALLS"`)
	executable(t, filepath.Join(root, "user/dark-mode.d/20-unified"), `echo old >> "$CALLS"`)
	executable(t, filepath.Join(root, "system/darkman/20-unified"), `printf 'unified:%s:%s:%s\n' "$#" "$1" "$WSL_DARKMAN_MODE" >> "$CALLS"`)
	executable(t, filepath.Join(root, "user/light-mode.d/other"), `echo unwanted >> "$CALLS"`)
	if !applyMode(context.Background(), dark, testOptions()) {
		t.Fatal("dark hooks failed")
	}
	if got := fileContents(t, os.Getenv("CALLS")); got != "legacy:0:dark\nunified:1:dark:dark\n" {
		t.Fatalf("hook arguments or precedence: %q", got)
	}
	if !applyMode(context.Background(), light, testOptions()) {
		t.Fatal("light hooks failed")
	}
	if got := fileContents(t, os.Getenv("CALLS")); !strings.Contains(got, "unified:1:light:light\n") {
		t.Fatalf("unified hook did not receive light: %q", got)
	}
}

func TestHookMaskingAndSystemOrder(t *testing.T) {
	root := testEnvironment(t)
	first := filepath.Join(root, "first")
	last := filepath.Join(root, "last")
	t.Setenv("XDG_DATA_DIRS", first+":"+last)
	executable(t, filepath.Join(first, "dark-mode.d/theme"), "exit 1")
	winner := executable(t, filepath.Join(last, "dark-mode.d/theme"), "exit 0")
	commands := hooksFor(dark)
	if len(commands) != 1 || commands[0][0] != winner {
		t.Fatalf("system order: %v", commands)
	}
	executable(t, filepath.Join(first, "darkman/theme"), "exit 1")
	mask := executable(t, filepath.Join(root, "user/darkman/theme"), "exit 1")
	if err := os.Chmod(mask, 0644); err != nil {
		t.Fatal(err)
	}
	if commands := hooksFor(dark); len(commands) != 0 {
		t.Fatalf("nonexecutable user entry did not mask both hook formats: %v", commands)
	}
}

func TestHookFailureAndTimeoutContinue(t *testing.T) {
	root := testEnvironment(t)
	t.Setenv("CALLS", filepath.Join(root, "calls"))
	t.Setenv("LEAK", filepath.Join(root, "leaked"))
	executable(t, filepath.Join(root, "user/dark-mode.d/10-fail"), "exit 7")
	executable(t, filepath.Join(root, "user/dark-mode.d/20-timeout"), `(sleep 0.3; echo leaked > "$LEAK") & wait`)
	executable(t, filepath.Join(root, "user/dark-mode.d/30-success"), `echo done >> "$CALLS"`)
	if applyMode(context.Background(), dark, testOptions()) {
		t.Fatal("hook failure was not reported")
	}
	if got := fileContents(t, os.Getenv("CALLS")); got != "done\n" {
		t.Fatalf("later hook was not executed: %q", got)
	}
	time.Sleep(350 * time.Millisecond)
	if _, err := os.Stat(os.Getenv("LEAK")); !os.IsNotExist(err) {
		t.Fatal("timed-out hook descendant was not killed")
	}
}

func TestGSettingsAndThemeIntegration(t *testing.T) {
	root := testEnvironment(t)
	t.Setenv("CALLS", filepath.Join(root, "calls"))
	t.Setenv("PATH", filepath.Join(root, "bin")+":"+os.Getenv("PATH"))
	executable(t, filepath.Join(root, "bin/gsettings"), `
if [ "$1" = list-keys ]; then
    printf 'color-scheme\ngtk-theme\n'
else
    printf '%s\n' "$*" >> "$CALLS"
fi`)
	executable(t, filepath.Join(root, "user/darkman/theme"), `echo hook >> "$CALLS"`)
	o := testOptions()
	o.noGSettings = false
	o.gtkLightTheme, o.gtkDarkTheme = "Adwaita", "Adwaita-dark"
	if !applyMode(context.Background(), dark, o) || !applyMode(context.Background(), light, o) {
		t.Fatal("gsettings integration failed")
	}
	if got := fileContents(t, os.Getenv("CALLS")); got != "set org.gnome.desktop.interface color-scheme prefer-dark\nset org.gnome.desktop.interface gtk-theme \"Adwaita-dark\"\nhook\nset org.gnome.desktop.interface color-scheme prefer-light\nset org.gnome.desktop.interface gtk-theme \"Adwaita\"\nhook\n" {
		t.Fatalf("gsettings calls: %q", got)
	}
	executable(t, filepath.Join(root, "bin/gsettings"), "exit 7")
	if applyMode(context.Background(), dark, o) {
		t.Fatal("explicit GTK theme failure was swallowed")
	}
}

func TestMissingGnomeSchemaDoesNotBlockHooks(t *testing.T) {
	root := testEnvironment(t)
	t.Setenv("CALLS", filepath.Join(root, "calls"))
	t.Setenv("PATH", filepath.Join(root, "bin")+":"+os.Getenv("PATH"))
	executable(t, filepath.Join(root, "bin/gsettings"), "exit 1")
	executable(t, filepath.Join(root, "user/darkman/theme"), `echo hook >> "$CALLS"`)
	o := testOptions()
	o.noGSettings = false
	if !applyMode(context.Background(), dark, o) || fileContents(t, os.Getenv("CALLS")) != "hook\n" {
		t.Fatal("missing GNOME schema blocked independent hook integration")
	}
}

func TestCancellationStopsHookAndRemainingActions(t *testing.T) {
	root := testEnvironment(t)
	t.Setenv("CALLS", filepath.Join(root, "calls"))
	executable(t, filepath.Join(root, "user/dark-mode.d/10-slow"), "sleep 5")
	executable(t, filepath.Join(root, "user/dark-mode.d/20-never"), `echo unwanted > "$CALLS"`)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	o := testOptions()
	o.hookTimeout = 5 * time.Second
	started := time.Now()
	if applyMode(ctx, dark, o) {
		t.Fatal("canceled actions reported success")
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancellation did not interrupt the running hook")
	}
	if _, err := os.Stat(os.Getenv("CALLS")); !os.IsNotExist(err) {
		t.Fatal("an action ran after cancellation")
	}
}
