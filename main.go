//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type mode string

const (
	light mode = "light"
	dark  mode = "dark"
)

type options struct {
	command, source, powershell, backend string
	noGSettings                          bool
	hookTimeout                          time.Duration
	startupTimeout, shutdownTimeout      time.Duration
	retryMin, retryMax                   time.Duration
}

func defaultOptions() options {
	return options{
		command: "run", source: "apps", backend: "standalone",
		hookTimeout: 10 * time.Second, startupTimeout: 30 * time.Second,
		shutdownTimeout: 5 * time.Second, retryMin: time.Second, retryMax: 30 * time.Second,
	}
}

func parseOptions(args []string, output io.Writer) (options, error) {
	o := defaultOptions()
	f := flag.NewFlagSet("wsl-darkman", flag.ContinueOnError)
	f.SetOutput(output)
	f.StringVar(&o.source, "source", o.source, "Windows appearance: apps or system")
	f.StringVar(&o.powershell, "powershell", "", "Windows powershell.exe path inside WSL")
	f.StringVar(&o.backend, "backend", o.backend, "Output backend: standalone or darkman")
	f.BoolVar(&o.noGSettings, "no-gsettings", false, "Skip the standalone GNOME preference update")
	seconds := f.Float64("hook-timeout", 10, "Seconds allowed per action")
	f.Usage = func() {
		fmt.Fprintln(output, "Usage: wsl-darkman [run|get|once] [options]")
		fmt.Fprintln(output, "Follow Windows appearance from WSL. Default command: run.")
		f.PrintDefaults()
	}
	// flag normally stops at a positional argument. Remove the command first,
	// retaining values belonging to options, to support both CLI orderings.
	var flags []string
	commandSeen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			name := strings.TrimLeft(arg, "-")
			if !strings.Contains(name, "=") {
				if entry := f.Lookup(name); entry != nil {
					boolean, ok := entry.Value.(interface{ IsBoolFlag() bool })
					if !(ok && boolean.IsBoolFlag()) && i+1 < len(args) {
						i++
						flags = append(flags, args[i])
					}
				}
			}
		} else if !commandSeen && (arg == "run" || arg == "get" || arg == "once") {
			o.command, commandSeen = arg, true
		} else {
			return o, fmt.Errorf("unexpected argument %q (expected run, get, or once)", arg)
		}
	}
	if err := f.Parse(flags); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, fmt.Errorf("unexpected arguments: %v", f.Args())
	}
	if o.source != "apps" && o.source != "system" {
		return o, fmt.Errorf("source must be apps or system")
	}
	if o.backend != "standalone" && o.backend != "darkman" {
		return o, fmt.Errorf("backend must be standalone or darkman")
	}
	if math.IsNaN(*seconds) || math.IsInf(*seconds, 0) || *seconds <= 0 ||
		*seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return o, fmt.Errorf("hook-timeout must be a finite positive duration")
	}
	o.hookTimeout = time.Duration(*seconds * float64(time.Second))
	if o.hookTimeout <= 0 {
		return o, fmt.Errorf("hook-timeout must be at least one nanosecond")
	}
	return o, nil
}

func parseMode(raw string) (mode, error) {
	switch m := mode(strings.TrimSpace(raw)); m {
	case dark, light:
		return m, nil
	default:
		return "", fmt.Errorf("unexpected Windows watcher output: %.100q", raw)
	}
}

func execute(ctx context.Context, o options, output io.Writer) error {
	if o.command == "run" {
		return runDaemon(ctx, o)
	}
	m, err := readMode(ctx, o)
	if err != nil {
		return err
	}
	if o.command == "get" {
		_, err = fmt.Fprintln(output, m)
		return err
	}
	if !applyMode(ctx, m, o) {
		return errors.New("one or more appearance actions failed")
	}
	return nil
}

func main() {
	log.SetPrefix("wsl-darkman: ")
	log.SetFlags(0)
	o, err := parseOptions(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Print(err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, o, os.Stdout); err != nil && ctx.Err() == nil {
		log.Print(err)
		os.Exit(1)
	}
}
