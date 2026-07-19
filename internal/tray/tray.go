//go:build linux

// Package tray shows a system-tray icon via the freedesktop StatusNotifierItem
// (SNI) protocol over DBus — no GTK/cgo required. It is best-effort: if no SNI
// host is running (e.g. GNOME without the AppIndicator extension) Run returns an
// error and the caller simply continues without a tray.
package tray

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"

	"gdrive-sync/internal/manager"
)

const (
	sniPath  = "/StatusNotifierItem"
	menuPath = "/StatusNotifierItem/menu"
	sniIface = "org.kde.StatusNotifierItem"
	menuIf   = "com.canonical.dbusmenu"
)

// Actions holds the callbacks invoked from the tray menu.
type Actions struct {
	OpenFolder   func()
	SyncNow      func()
	TogglePause  func()
	OpenSettings func()
	Logout       func()
	Quit         func()
}

type iconPix struct {
	W, H  int32
	Bytes []byte
}

// Run installs the tray icon and blocks until ctx is cancelled. It returns an
// error if no tray host accepted the registration.
func Run(ctx context.Context, mgr *manager.Manager, act Actions, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("session bus: %w", err)
	}

	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
	if _, err := conn.RequestName(name, dbus.NameFlagDoNotQueue); err != nil {
		return fmt.Errorf("request name: %w", err)
	}

	menu := newMenu(act)
	menu.conn = conn
	item := &snItem{act: act}

	if err := conn.Export(item, sniPath, sniIface); err != nil {
		return err
	}
	if err := conn.Export(menu, menuPath, menuIf); err != nil {
		return err
	}

	sniProps, err := prop.Export(conn, sniPath, item.propSpec())
	if err != nil {
		return err
	}
	if _, err := prop.Export(conn, menuPath, menu.propSpec()); err != nil {
		return err
	}
	item.props = sniProps
	item.menu = menu
	item.conn = conn

	// Register with the StatusNotifierWatcher.
	watcher := conn.Object("org.kde.StatusNotifierWatcher", "/StatusNotifierWatcher")
	if call := watcher.Call("org.kde.StatusNotifierWatcher.RegisterStatusNotifierItem", 0, name); call.Err != nil {
		return fmt.Errorf("no tray host: %w", call.Err)
	}
	logf("tray registered as %s", name)

	// Reflect manager status into the icon, tooltip and menu.
	mgr.Subscribe(func(st manager.Status) {
		item.update(st)
	})

	<-ctx.Done()
	return nil
}

// ---------------- StatusNotifierItem ----------------

type snItem struct {
	act   Actions
	conn  *dbus.Conn
	props *prop.Properties
	menu  *dbusMenu

	mu    sync.Mutex
	state manager.State
}

func (s *snItem) propSpec() map[string]map[string]*prop.Prop {
	r, g, b := colorFor(manager.StateDisconnected)
	return map[string]map[string]*prop.Prop{
		sniIface: {
			"Category":   {Value: "ApplicationStatus", Writable: false},
			"Id":         {Value: "gdrive-sync", Writable: false},
			"Title":      {Value: "Google Drive Sync", Writable: false},
			"Status":     {Value: "Active", Writable: false},
			"WindowId":   {Value: int32(0), Writable: false},
			"IconName":   {Value: "", Writable: false},
			"IconPixmap": {Value: makeIcon(r, g, b), Writable: false},
			"ToolTip":    {Value: makeToolTip("Google Drive Sync", "Nicht angemeldet"), Writable: false},
			"ItemIsMenu": {Value: true, Writable: false},
			"Menu":       {Value: dbus.ObjectPath(menuPath), Writable: false},
		},
	}
}

// Activate is emitted on a primary (left) click.
func (s *snItem) Activate(x, y int32) *dbus.Error {
	if s.act.OpenSettings != nil {
		s.act.OpenSettings()
	}
	return nil
}

// SecondaryActivate is emitted on a middle click.
func (s *snItem) SecondaryActivate(x, y int32) *dbus.Error { return nil }

// ContextMenu is emitted on a right click (host usually shows Menu itself).
func (s *snItem) ContextMenu(x, y int32) *dbus.Error { return nil }

// Scroll is emitted on wheel scroll over the icon.
func (s *snItem) Scroll(delta int32, orientation string) *dbus.Error { return nil }

func (s *snItem) update(st manager.Status) {
	s.mu.Lock()
	changed := s.state != st.State
	s.state = st.State
	s.mu.Unlock()

	if s.props != nil {
		r, g, b := colorFor(st.State)
		s.props.SetMust(sniIface, "IconPixmap", makeIcon(r, g, b))
		tip := st.Message
		if st.Account != "" {
			tip = st.Account + " — " + st.Message
		}
		s.props.SetMust(sniIface, "ToolTip", makeToolTip("Google Drive Sync", tip))
	}
	if changed && s.conn != nil {
		_ = s.conn.Emit(sniPath, sniIface+".NewIcon")
		_ = s.conn.Emit(sniPath, sniIface+".NewToolTip")
	}
	if s.menu != nil {
		s.menu.updateFromStatus(st)
	}
}

func makeToolTip(title, sub string) []interface{} {
	// (s a(iiay) s s) => iconName, iconPixmaps, title, description
	return []interface{}{"", []iconPix{}, title, sub}
}

// makeIcon renders a 22x22 filled status circle as ARGB32 (network byte order).
func makeIcon(r, g, b byte) []iconPix {
	const n = 22
	buf := make([]byte, n*n*4)
	cx, cy, rad := 10.5, 10.5, 9.0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			i := (y*n + x) * 4
			if dx*dx+dy*dy <= rad*rad {
				buf[i] = 0xff // A
				buf[i+1] = r
				buf[i+2] = g
				buf[i+3] = b
			}
		}
	}
	return []iconPix{{W: n, H: n, Bytes: buf}}
}

func colorFor(s manager.State) (byte, byte, byte) {
	switch s {
	case manager.StateIdle:
		return 0x18, 0x80, 0x38 // green
	case manager.StateSyncing, manager.StateStarting:
		return 0x1a, 0x73, 0xe8 // blue
	case manager.StateError:
		return 0xd9, 0x30, 0x25 // red
	default:
		return 0x9a, 0xa0, 0xa6 // grey
	}
}
