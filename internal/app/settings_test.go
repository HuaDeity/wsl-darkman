//go:build linux

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGSettingsProbeTimeoutIsReported(t *testing.T) {
	root := testEnvironment(t)
	t.Setenv("PATH", filepath.Join(root, "bin")+":"+os.Getenv("PATH"))
	executable(t, filepath.Join(root, "bin/gsettings"), "sleep 5")
	o := testOptions()
	o.noGSettings = false
	if applyGSettings(context.Background(), dark, o) {
		t.Fatal("timed-out capability query was mistaken for an absent optional schema")
	}
}

func TestRealGSettingsWithIsolatedDconf(t *testing.T) {
	if _, err := exec.LookPath("gsettings"); err != nil {
		t.Skip("real settings integration requires gsettings")
	}
	root := testEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := exec.LookPath("glib-compile-schemas"); err != nil {
		t.Skip("real settings integration requires glib-compile-schemas")
	}
	schemaDir := filepath.Join(root, "schemas")
	if err := os.MkdirAll(schemaDir, 0700); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("testdata/org.gnome.desktop.interface.gschema.xml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(schemaDir, "org.gnome.desktop.interface.gschema.xml"), schema, 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := exec.CommandContext(ctx, "glib-compile-schemas", schemaDir).CombinedOutput(); err != nil {
		t.Fatalf("compile schema fixture: %v: %s", err, data)
	}
	t.Setenv("GSETTINGS_SCHEMA_DIR", schemaDir)
	if _, err := os.Stat("/usr/libexec/dconf-service"); err != nil {
		if _, err := os.Stat("/usr/lib/dconf/dconf-service"); err != nil {
			t.Skip("real settings integration requires dconf-service")
		}
	}
	// The bus daemon passes this isolated environment to the dconf service.
	// Both the database name and its configuration directory are test-specific.
	profile := filepath.Join(root, "dconf-profile")
	if err := os.WriteFile(profile, []byte("user-db:wsldarkmantest\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DCONF_PROFILE", profile)
	t.Setenv("GSETTINGS_BACKEND", "dconf")
	address, _ := privateBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	o := testOptions()
	o.noGSettings = false
	o.hookTimeout = 2 * time.Second
	o.gtkLightTheme, o.gtkDarkTheme = "Test Light Theme", "Test Dark Theme"
	for _, m := range []mode{dark, light} {
		if !applyGSettings(ctx, m, o) {
			t.Fatalf("could not apply %s mode to real GSettings", m)
		}
		for key, want := range map[string]string{
			"color-scheme": "'prefer-" + string(m) + "'",
			"gtk-theme":    "'Test " + strings.ToUpper(string(m[:1])) + string(m[1:]) + " Theme'",
		} {
			data, err := exec.CommandContext(ctx, "gsettings", "get", interfaceSchema, key).CombinedOutput()
			if err != nil || strings.TrimSpace(string(data)) != want {
				t.Fatalf("%s after %s mode: %s (%v), expected %s", key, m, data, err, want)
			}
		}
	}
}
