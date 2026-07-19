package window

import (
	"os"
	"path/filepath"
)

// InstallDesktopEntry writes a user-scope .desktop file and the app icon into
// ~/.local/share so the Wayland compositor can associate the settings window
// (whose app_id is "gdrive-sync") with its icon. On Wayland a GTK3 app cannot
// hand a per-window icon to the compositor, so this desktop-file match is the
// only reliable way to get the logo into the titlebar / taskbar / overview.
//
// It is idempotent (only writes when content changed) and best-effort: any
// error is returned for logging but is not fatal to the daemon.
func InstallDesktopEntry() error {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		data = filepath.Join(home, ".local", "share")
	}

	iconDir := filepath.Join(data, "icons", "hicolor", "scalable", "apps")
	if err := os.MkdirAll(iconDir, 0o755); err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(iconDir, "gdrive-sync.svg"), iconSVG); err != nil {
		return err
	}

	// Prefer the outer AppImage path (stable across runs) over the executable,
	// which for an AppImage points into a temporary mount that vanishes on exit.
	exec := os.Getenv("APPIMAGE")
	if exec == "" {
		if e, err := os.Executable(); err == nil {
			exec = e
		} else {
			exec = "gdrive-sync"
		}
	}

	appDir := filepath.Join(data, "applications")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return err
	}
	// The file id ("gdrive-sync") must equal the window's Wayland app_id for the
	// compositor to match them; StartupWMClass covers X11 as well.
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Google Drive Sync\n" +
		"GenericName=Cloud-Synchronisation\n" +
		"Comment=Google Drive mit dem Rechner synchronisieren\n" +
		"Exec=\"" + exec + "\"\n" +
		"Icon=gdrive-sync\n" +
		"Terminal=false\n" +
		"Categories=Network;FileTransfer;\n" +
		"Keywords=google;drive;sync;cloud;backup;\n" +
		"StartupNotify=false\n" +
		"StartupWMClass=gdrive-sync\n"
	return writeIfChanged(filepath.Join(appDir, "gdrive-sync.desktop"), []byte(entry))
}

// writeIfChanged writes data to path only when it differs from the current
// contents, so repeated daemon starts do not churn the files.
func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}
