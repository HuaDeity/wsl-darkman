//go:build linux

package app

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOptions() options {
	o := defaultOptions()
	o.noGSettings = true
	o.noPortal = true
	o.hookTimeout = 100 * time.Millisecond
	o.startupTimeout = 2 * time.Second
	o.shutdownTimeout = 2 * time.Second
	o.retryMin, o.retryMax = 10*time.Millisecond, 20*time.Millisecond
	return o
}

func testEnvironment(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, subdir := range map[string]string{
		"XDG_DATA_HOME": "user", "XDG_DATA_DIRS": "system",
		"XDG_RUNTIME_DIR": "runtime", "XDG_CACHE_HOME": "cache",
		"XDG_CONFIG_HOME": "config",
	} {
		path := filepath.Join(root, subdir)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(name, path)
	}
	return root
}

func executable(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileContents(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCLIOptions(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--source", "system", "--no-gsettings", "--hook-timeout", "0.5"},
		{"--source=system", "--no-gsettings", "--hook-timeout=0.5", "run"},
		{"--source", "system", "run", "--no-gsettings", "--hook-timeout", "0.5"},
	} {
		o, err := parseOptions(args, io.Discard)
		if err != nil || o.source != "system" || !o.noGSettings || o.hookTimeout != 500*time.Millisecond {
			t.Fatalf("parse %v: %+v, %v", args, o, err)
		}
	}
	for _, args := range [][]string{
		{"run", "get"}, {"invalid"}, {"--source", "other"}, {"--backend", "darkman"},
		{"--gtk-light-theme", "Adwaita"}, {"--gtk-dark-theme", "Adwaita-dark"},
		{"--no-gsettings", "--gtk-light-theme", "Adwaita", "--gtk-dark-theme", "Adwaita-dark"},
		{"--hook-timeout", "0"}, {"--hook-timeout", "-1"}, {"--hook-timeout", "NaN"},
		{"--hook-timeout", "Inf"}, {"--hook-timeout", "1e50"}, {"--hook-timeout", "1e-50"},
		{"--source"}, {"--unknown"},
	} {
		if _, err := parseOptions(args, io.Discard); err == nil {
			t.Errorf("accepted invalid arguments %v", args)
		}
	}
	if _, err := parseOptions([]string{"--help"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help: %v", err)
	}
	o, err := parseOptions([]string{"get", "--powershell", "run"}, io.Discard)
	if err != nil || o.command != "get" || o.powershell != "run" {
		t.Fatalf("option value treated as command: %+v, %v", o, err)
	}
}

func TestParseHostModes(t *testing.T) {
	for _, raw := range []string{"dark\r\n", "light\n"} {
		if _, err := parseMode(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"", "0", "dark\nlight", "broken"} {
		if _, err := parseMode(raw); err == nil {
			t.Errorf("accepted invalid mode %q", raw)
		}
	}
}

// The test executable doubles as a simulated Windows process. Tests exercise
// actual pipes, EOF, process exits, and signals without Python or a Windows host.
func helperCommand(t *testing.T, action string, args ...string) *exec.Cmd {
	t.Helper()
	arguments := append([]string{"-test.run=^TestProcessHelper$", "--", action}, args...)
	cmd := exec.Command(os.Args[0], arguments...)
	cmd.Env = setEnvironment(os.Environ(), "WSL_DARKMAN_TEST_HELPER", "1")
	cmd.Env = setEnvironment(cmd.Env, "GORACE", "atexit_sleep_ms=0")
	return cmd
}

func fakeHost(t *testing.T, root, action string) string {
	t.Helper()
	t.Setenv("WSL_DARKMAN_TEST_HELPER", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("HELPER_BINARY", os.Args[0])
	t.Setenv("HELPER_ACTION", action)
	return executable(t, filepath.Join(root, "powershell.exe"),
		`exec "$HELPER_BINARY" -test.run='^TestProcessHelper$' -- "$HELPER_ACTION"`)
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("WSL_DARKMAN_TEST_HELPER") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == 0 || separator+1 >= len(os.Args) {
		os.Exit(9)
	}
	switch os.Args[separator+1] {
	case "stream":
		fmt.Print("dark\r\ndark\r\nlight\r\nlight\r\n")
		io.Copy(io.Discard, os.Stdin)
	case "idle":
		if path := os.Getenv("HELPER_READY"); path != "" {
			if err := os.WriteFile(path, []byte("ready"), 0600); err != nil {
				os.Exit(8)
			}
		}
		io.Copy(io.Discard, os.Stdin)
	case "stubborn":
		fmt.Println("dark")
		time.Sleep(time.Hour)
	case "query":
		fmt.Println("dark")
	case "eof":
		fmt.Println("dark")
	case "invalid":
		fmt.Println("bad-mode")
		io.Copy(io.Discard, os.Stdin)
	case "failure":
		fmt.Fprintln(os.Stderr, "simulated Windows failure")
		os.Exit(7)
	case "cli":
		os.Exit(Run(os.Args[separator+2:], os.Stdout, os.Stderr))
	default:
		os.Exit(9)
	}
	if path := os.Getenv("HELPER_EXIT"); path != "" {
		if err := os.WriteFile(path, []byte("closed"), 0600); err != nil {
			os.Exit(8)
		}
	}
	os.Exit(0)
}

func TestGetAndOnceCommands(t *testing.T) {
	root := testEnvironment(t)
	o := testOptions()
	o.powershell = fakeHost(t, root, "query")
	o.command = "get"
	var output bytes.Buffer
	if err := execute(context.Background(), o, &output); err != nil || output.String() != "dark\n" {
		t.Fatalf("get output=%q error=%v", output.String(), err)
	}
	o.command = "once"
	executable(t, filepath.Join(root, "user/dark-mode.d/fail"), "exit 7")
	if err := execute(context.Background(), o, io.Discard); err == nil {
		t.Fatal("once did not propagate a hook failure")
	}
	t.Setenv("HELPER_ACTION", "failure")
	if _, err := readMode(context.Background(), o); err == nil || !strings.Contains(err.Error(), "simulated Windows failure") {
		t.Fatalf("host diagnostic was lost: %v", err)
	}
}
