//go:build linux

package app

import (
	"context"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

const interfaceSchema = "org.gnome.desktop.interface"

// GNOME's color preference and its named GTK theme are separate settings.
// Probe installed keys so a minimal WSL distribution can still use the portal
// and hooks without requiring GNOME's settings schemas.
func applyGSettings(ctx context.Context, m mode, o options) bool {
	theme := o.gtkLightTheme
	if m == dark {
		theme = o.gtkDarkTheme
	}
	if _, err := exec.LookPath("gsettings"); err != nil {
		log.Print("gsettings unavailable; skipping GNOME preferences")
		return theme == ""
	}
	queryCtx, cancel := context.WithTimeout(ctx, o.hookTimeout)
	defer cancel()
	cmd := commandWithCancellation(queryCtx, []string{"gsettings", "list-keys", interfaceSchema})
	output, err := cmd.CombinedOutput()
	if err != nil {
		if queryCtx.Err() != nil {
			log.Printf("Query GNOME settings: %v", queryCtx.Err())
			return false
		}
		log.Printf("GNOME interface schema unavailable: %s", strings.TrimSpace(string(output)))
		return theme == "" && ctx.Err() == nil
	}
	keys := make(map[string]bool)
	for _, key := range strings.Fields(string(output)) {
		keys[key] = true
	}
	ok := true
	if keys["color-scheme"] {
		ok = runAction(ctx, []string{"gsettings", "set", interfaceSchema, "color-scheme", "prefer-" + string(m)}, o.hookTimeout, nil)
	} else {
		log.Print("GNOME color-scheme key unavailable; using the portal and hooks")
	}
	if theme != "" {
		if !keys["gtk-theme"] {
			log.Print("Cannot apply the requested GTK theme: gtk-theme key unavailable")
			return false
		}
		// Quote as a GVariant string, including theme names containing spaces.
		if !runAction(ctx, []string{"gsettings", "set", interfaceSchema, "gtk-theme", strconv.Quote(theme)}, o.hookTimeout, nil) {
			ok = false
		}
	}
	return ok
}
