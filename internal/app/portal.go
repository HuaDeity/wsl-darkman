//go:build linux

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	portalBusName   = "org.freedesktop.impl.portal.desktop.wsl_darkman"
	portalPath      = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	portalInterface = "org.freedesktop.impl.portal.Settings"
	appearanceNS    = "org.freedesktop.appearance"
	colorSchemeKey  = "color-scheme"
)

// The portal is a read-only view of Windows appearance. It does not implement
// Darkman's control API or change the Windows source of truth.
type settingsPortal struct {
	conn  *dbus.Conn
	mu    sync.RWMutex
	value uint32 // XDG values: 0 unknown/no preference, 1 dark, 2 light.
}

// Only these two methods are exported on the settings D-Bus interface.
type settingsAPI struct{ portal *settingsPortal }

func sessionBusAddress() string {
	if address := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); address != "" {
		return address
	}
	if runtime := os.Getenv("XDG_RUNTIME_DIR"); runtime != "" {
		path := filepath.Join(runtime, "bus")
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return "unix:path=" + dbus.EscapeBusAddressValue(path)
		}
	}
	return ""
}

func startSettingsPortal(ctx context.Context, address string) (*settingsPortal, error) {
	if address == "" {
		return nil, fmt.Errorf("no user session D-Bus address available")
	}
	conn, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	p := &settingsPortal{conn: conn}
	if err := p.export(); err != nil {
		conn.Close()
		return nil, err
	}
	return p, nil
}

func (p *settingsPortal) export() error {
	if err := p.conn.Export(&settingsAPI{portal: p}, portalPath, portalInterface); err != nil {
		return err
	}
	properties, err := prop.Export(p.conn, portalPath, prop.Map{
		portalInterface: {
			"version": {Value: uint32(1), Writable: false, Emit: prop.EmitFalse},
		},
	})
	if err != nil {
		return err
	}
	node := &introspect.Node{
		Name: string(portalPath),
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name:       portalInterface,
				Methods:    introspect.Methods(&settingsAPI{}),
				Properties: properties.Introspection(portalInterface),
				Signals: []introspect.Signal{{
					Name: "SettingChanged",
					Args: []introspect.Arg{{Name: "namespace", Type: "s"}, {Name: "key", Type: "s"}, {Name: "value", Type: "v"}},
				}},
			},
		},
	}
	if err := p.conn.Export(introspect.NewIntrospectable(node), portalPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return err
	}
	reply, err := p.conn.RequestName(portalBusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("appearance portal bus name is already owned")
	}
	return nil
}

func (p *settingsPortal) current() uint32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.value
}

func (p *settingsPortal) update(m mode) error {
	var value uint32
	switch m {
	case dark:
		value = 1
	case light:
		value = 2
	default:
		return fmt.Errorf("invalid appearance %q", m)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.value == value {
		return nil
	}
	p.value = value
	return p.conn.Emit(portalPath, portalInterface+".SettingChanged", appearanceNS, colorSchemeKey, dbus.MakeVariant(value))
}

func (api *settingsAPI) Read(namespace, key string) (dbus.Variant, *dbus.Error) {
	if namespace != appearanceNS || key != colorSchemeKey {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.portal.Error.NotFound", []interface{}{"Unknown appearance setting"})
	}
	return dbus.MakeVariant(api.portal.current()), nil
}

func (api *settingsAPI) ReadAll(namespaces []string) (map[string]map[string]dbus.Variant, *dbus.Error) {
	result := make(map[string]map[string]dbus.Variant)
	matched := len(namespaces) == 0
	for _, namespace := range namespaces {
		if namespace == "" || namespace == "*" || namespace == appearanceNS ||
			(strings.HasSuffix(namespace, ".*") && strings.HasPrefix(appearanceNS, strings.TrimSuffix(namespace, "*"))) {
			matched = true
		}
	}
	if matched {
		result[appearanceNS] = map[string]dbus.Variant{colorSchemeKey: dbus.MakeVariant(api.portal.current())}
	}
	return result, nil
}
