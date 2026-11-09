//go:build linux

package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestWindowsCommandEncoding(t *testing.T) {
	o := testOptions()
	o.powershell, o.source = "/custom/powershell.exe", "system"
	cmd, err := windowsCommand(o, true)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != o.powershell {
		t.Fatalf("ignored explicit executable: %s", cmd.Path)
	}
	data, err := base64.StdEncoding.DecodeString(cmd.Args[len(cmd.Args)-1])
	if err != nil || len(data)%2 != 0 {
		t.Fatalf("invalid encoded command: %v", err)
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	script := string(utf16.Decode(units))
	if !strings.Contains(script, "::Run('SystemUsesLightTheme', $true)") || strings.Contains(script, "__VALUE__") {
		t.Fatalf("unsubstituted or wrong host request: %s", script)
	}
	if !strings.Contains(script, "RegNotifyChangeKeyValue") {
		t.Fatal("native registry watcher was not embedded")
	}
}

func TestInteropSocketDiscovery(t *testing.T) {
	root := t.TempDir()
	var paths []string
	for _, name := range []string{"old_interop", "new_interop"} {
		path := filepath.Join(root, name)
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		paths = append(paths, path)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(paths[0], old, old); err != nil {
		t.Fatal(err)
	}
	base := []string{"OTHER=value", "WSL_INTEROP=/missing/socket"}
	env := interopEnvironment(base, root)
	if got := environmentValue(env, "WSL_INTEROP"); got != paths[1] {
		t.Fatalf("did not recover the newest socket: %q", got)
	}
	env = interopEnvironment([]string{"WSL_INTEROP=" + paths[0]}, root)
	if got := environmentValue(env, "WSL_INTEROP"); got != paths[0] {
		t.Fatalf("overrode a valid inherited socket: %q", got)
	}
	if got := environmentValue(base, "WSL_INTEROP"); got != "/missing/socket" {
		t.Fatal("modified the caller's environment")
	}
}

func TestQueryTimeout(t *testing.T) {
	root := testEnvironment(t)
	o := testOptions()
	o.powershell = fakeHost(t, root, "stubborn")
	o.startupTimeout = 100 * time.Millisecond
	started := time.Now()
	if _, err := readMode(context.Background(), o); err == nil {
		t.Fatal("accepted a query process that never exits")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("query timeout did not stop the process")
	}
}
