//go:build linux

package app

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// A private bus keeps tests independent of the user's desktop and settings.
func privateBus(t *testing.T) (string, func()) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("D-Bus integration tests require dbus-daemon")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1", "--address=unix:tmpdir="+t.TempDir())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() { once.Do(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() }) }
	t.Cleanup(stop)
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(address), stop
}

func TestPortalReadsFiltersSignalsAndOwnership(t *testing.T) {
	address, _ := privateBus(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	portal, err := startSettingsPortal(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer portal.conn.Close()
	client, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	object := client.Object(portalBusName, portalPath)
	read := func() uint32 {
		t.Helper()
		var value dbus.Variant
		if err := object.CallWithContext(ctx, portalInterface+".Read", 0, appearanceNS, colorSchemeKey).Store(&value); err != nil {
			t.Fatal(err)
		}
		result, ok := value.Value().(uint32)
		if !ok {
			t.Fatalf("color scheme has wrong D-Bus type: %T", value.Value())
		}
		return result
	}
	if value := read(); value != 0 {
		t.Fatalf("initial mode must be unknown: %d", value)
	}
	if err := client.AddMatchSignal(dbus.WithMatchInterface(portalInterface), dbus.WithMatchMember("SettingChanged")); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, 8)
	client.Signal(signals)
	defer client.RemoveSignal(signals)
	for _, m := range []mode{dark, dark, light} {
		if err := portal.update(m); err != nil {
			t.Fatal(err)
		}
	}
	if value := read(); value != 2 {
		t.Fatalf("light must be XDG value 2: %d", value)
	}
	for _, want := range []uint32{1, 2} {
		select {
		case signal := <-signals:
			if len(signal.Body) != 3 || signal.Body[0] != appearanceNS || signal.Body[1] != colorSchemeKey || signal.Body[2].(dbus.Variant).Value() != want {
				t.Fatalf("invalid change signal: %+v", signal)
			}
		case <-ctx.Done():
			t.Fatal("missing appearance change signal")
		}
	}
	select {
	case duplicate := <-signals:
		t.Fatalf("duplicate mode emitted another signal: %+v", duplicate)
	default:
	}
	for _, filter := range [][]string{nil, {""}, {"*"}, {appearanceNS}, {"org.freedesktop.*"}, {"unrelated"}} {
		var values map[string]map[string]dbus.Variant
		if err := object.CallWithContext(ctx, portalInterface+".ReadAll", 0, filter).Store(&values); err != nil {
			t.Fatal(err)
		}
		if len(filter) == 1 && filter[0] == "unrelated" {
			if len(values) != 0 {
				t.Fatalf("ignored namespace filter: %v", values)
			}
		} else if values[appearanceNS][colorSchemeKey].Value() != uint32(2) {
			t.Fatalf("ReadAll(%v): %v", filter, values)
		}
	}
	err = object.CallWithContext(ctx, portalInterface+".Read", 0, appearanceNS, "unknown").Err
	if dbusError, ok := err.(dbus.Error); !ok || dbusError.Name != "org.freedesktop.portal.Error.NotFound" {
		t.Fatalf("unknown key did not return NotFound: %v", err)
	}
	var version dbus.Variant
	if err := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, portalInterface, "version").Store(&version); err != nil || version.Value() != uint32(1) {
		t.Fatalf("wrong portal version property: %v, %v", version, err)
	}
	var xml string
	if err := object.CallWithContext(ctx, "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil || !strings.Contains(xml, `name="SettingChanged"`) || !strings.Contains(xml, `name="version" type="u" access="read"`) {
		t.Fatalf("incomplete introspection: %v: %s", err, xml)
	}
	if other, err := startSettingsPortal(ctx, address); err == nil {
		other.conn.Close()
		t.Fatal("a second portal stole the existing bus name")
	}
}

func TestDaemonPublishesWindowsAppearance(t *testing.T) {
	root := testEnvironment(t)
	address, _ := privateBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	o := testOptions()
	o.noPortal = false
	o.powershell = fakeHost(t, root, "stream")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.AddMatchSignal(dbus.WithMatchInterface(portalInterface), dbus.WithMatchMember("SettingChanged")); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, 8)
	client.Signal(signals)
	defer client.RemoveSignal(signals)
	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx, o) }()
	for _, want := range []uint32{1, 2} {
		select {
		case signal := <-signals:
			if signal.Body[2].(dbus.Variant).Value() != want {
				t.Fatalf("wrong Windows transition: %v", signal.Body)
			}
		case err := <-done:
			t.Fatalf("daemon stopped before publishing appearance: %v", err)
		case <-ctx.Done():
			t.Fatal("host appearance was not published")
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDaemonStopsCleanlyOnSessionBusLoss(t *testing.T) {
	root := testEnvironment(t)
	address, stopBus := privateBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	exited := filepath.Join(root, "host-exited")
	t.Setenv("HELPER_EXIT", exited)
	ready := filepath.Join(root, "host-ready")
	t.Setenv("HELPER_READY", ready)
	o := testOptions()
	o.noPortal = false
	o.powershell = fakeHost(t, root, "idle")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx, o) }()
	client, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for {
		var owned bool
		if err := client.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, portalBusName).Store(&owned); err != nil {
			t.Fatal(err)
		}
		_, readyErr := os.Stat(ready)
		if owned && readyErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("daemon did not claim its portal name")
		case <-time.After(5 * time.Millisecond):
		}
	}
	stopBus()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "disconnected") {
			t.Fatalf("missing bus disconnect error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("daemon did not stop after its session bus disconnected")
	}
	if fileContents(t, exited) != "closed" {
		t.Fatal("session bus loss did not close the Windows watcher")
	}
}
