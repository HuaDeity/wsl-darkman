//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWatcherDeduplicationAndGracefulEOF(t *testing.T) {
	root := testEnvironment(t)
	marker := filepath.Join(root, "exited")
	t.Setenv("HELPER_EXIT", marker)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var seen []mode
	err := followWatcher(ctx, helperCommand(t, "stream"), testOptions(), func(_ context.Context, m mode) bool {
		seen = append(seen, m)
		if m == light {
			cancel()
		}
		return true
	})
	if err != nil || !reflect.DeepEqual(seen, []mode{dark, light}) {
		t.Fatalf("mode transitions %v, error %v", seen, err)
	}
	if got := fileContents(t, marker); got != "closed" {
		t.Fatalf("host did not receive EOF: %q", got)
	}
}

func TestWatcherFailures(t *testing.T) {
	for _, action := range []string{"eof", "invalid"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := followWatcher(ctx, helperCommand(t, action), testOptions(), func(context.Context, mode) bool { return true })
			if err == nil {
				t.Fatalf("did not report %s", action)
			}
			if action == "eof" && !errors.Is(err, io.EOF) {
				t.Fatalf("lost EOF: %v", err)
			}
		})
	}
}

func TestWatcherStartupTimeoutAndCancellation(t *testing.T) {
	for _, timedOut := range []bool{true, false} {
		o := testOptions()
		o.startupTimeout = 100 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		if !timedOut {
			cancel()
		}
		started := time.Now()
		err := followWatcher(ctx, helperCommand(t, "idle"), o, func(context.Context, mode) bool {
			t.Fatal("idle host emitted a mode")
			return false
		})
		cancel()
		if timedOut && (err == nil || !strings.Contains(err.Error(), "initial mode")) {
			t.Fatalf("startup timeout: %v", err)
		}
		if !timedOut && err != nil {
			t.Fatalf("canceled startup: %v", err)
		}
		if time.Since(started) > 2*time.Second {
			t.Fatal("startup could not be interrupted")
		}
	}
}

func TestWatcherForcedShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := testOptions()
	o.shutdownTimeout = 50 * time.Millisecond
	started := time.Now()
	err := followWatcher(ctx, helperCommand(t, "stubborn"), o, func(context.Context, mode) bool {
		cancel()
		return true
	})
	if err != nil || time.Since(started) > 2*time.Second {
		t.Fatalf("could not stop uncooperative host: %v", err)
	}
}

func TestFailedBackendReconnectsAndResynchronizes(t *testing.T) {
	o := testOptions()
	o.backend = "darkman"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var attempts int
	var seen []mode
	reconnect(ctx, o, func(ctx context.Context) error {
		attempts++
		return followWatcher(ctx, helperCommand(t, "stream"), o, func(_ context.Context, m mode) bool {
			seen = append(seen, m)
			if attempts == 1 {
				return false
			}
			cancel()
			return true
		})
	})
	if attempts != 2 || !reflect.DeepEqual(seen, []mode{dark, dark}) {
		t.Fatalf("did not resend host mode after backend failure: attempts=%d modes=%v", attempts, seen)
	}
}

func TestReconnectWaitCanBeCanceled(t *testing.T) {
	o := testOptions()
	o.retryMin = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	reconnect(ctx, o, func(context.Context) error { return errors.New("offline") })
	if time.Since(started) > time.Second {
		t.Fatal("shutdown waited for reconnection backoff")
	}
}

func TestSingletonLock(t *testing.T) {
	testEnvironment(t)
	first, err := acquireLock()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := acquireLock(); err == nil {
		second.Close()
		t.Fatal("duplicate daemon acquired the lock")
	}
	first.Close()
	third, err := acquireLock()
	if err != nil {
		t.Fatalf("lock not released on exit: %v", err)
	}
	third.Close()
}

func TestCLISIGTERMStopsDaemonAndHost(t *testing.T) {
	root := testEnvironment(t)
	ready, exited := filepath.Join(root, "ready"), filepath.Join(root, "exited")
	t.Setenv("READY", ready)
	t.Setenv("HELPER_EXIT", exited)
	host := fakeHost(t, root, "stream")
	executable(t, filepath.Join(root, "user/dark-mode.d/ready"), `touch "$READY"`)
	cmd := helperCommand(t, "cli", "run", "--powershell", host, "--no-gsettings")
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon never applied its initial mode")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SIGTERM exit: %v: %s", err, diagnostics.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not exit on SIGTERM")
	}
	if got := fileContents(t, exited); got != "closed" {
		t.Fatalf("host not closed: %q", got)
	}
}
