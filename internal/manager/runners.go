package manager

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// runStream mounts the whole Drive and keeps offline-pinned paths warm. It
// restarts the mount if it dies, until ctx is cancelled.
func (m *Manager) runStream(ctx context.Context) {
	mp := m.cfg.LocalDir
	if err := ensureDir(mp); err != nil {
		m.setState(StateError, "Mount-Ordner nicht nutzbar: "+err.Error())
		return
	}
	go m.pollStats(ctx)

	firstReady := false
	for ctx.Err() == nil {
		m.setState(StateStarting, "Google Drive wird eingebunden…")
		cmd, done := m.startProc(m.rc.MountArgs(mp, m.rcAddr, m.cacheDir), "mount")

		if m.waitMountReady(ctx, 40*time.Second) {
			m.setState(StateIdle, "Auf dem neuesten Stand")
			if !firstReady {
				m.notifier.Notify("Google Drive Sync", "Laufwerk bereit unter "+mp)
				firstReady = true
			}
			go m.warmOffline(ctx)
		}

		select {
		case <-ctx.Done():
			m.terminate(cmd)
			m.unmount(mp)
			<-done
			return
		case <-done:
			m.unmount(mp)
			if ctx.Err() != nil {
				return
			}
			m.setState(StateError, "Verbindung unterbrochen – Neustart in 5 s…")
		}
		if sleepCtx(ctx, 5*time.Second) {
			return
		}
	}
}

// runMirror keeps a two-way-synced local copy using rclone bisync, on a timer
// and on demand.
func (m *Manager) runMirror(ctx context.Context) {
	local := m.cfg.LocalDir
	if err := ensureDir(local); err != nil {
		m.setState(StateError, "Sync-Ordner nicht nutzbar: "+err.Error())
		return
	}
	marker := filepath.Join(m.cacheDir, "bisync-init-"+sanitize(local))

	for ctx.Err() == nil {
		first := !fileExists(marker)
		if first {
			m.setState(StateSyncing, "Erstabgleich läuft (kann dauern)…")
		} else {
			m.setState(StateSyncing, "Synchronisiere…")
		}

		cmd, done := m.startProc(m.rc.BisyncArgs(local, first), "bisync")
		var runErr error
		select {
		case <-ctx.Done():
			m.terminate(cmd)
			<-done
			return
		case runErr = <-done:
		}

		if runErr == nil {
			if first {
				_ = os.WriteFile(marker, []byte("ok\n"), 0o644)
			}
			m.mu.Lock()
			m.status.LastSync = time.Now()
			m.mu.Unlock()
			m.setState(StateIdle, "Auf dem neuesten Stand")
		} else {
			m.setState(StateError, "Synchronisierungsfehler – neuer Versuch folgt")
			m.notifier.Notify("Google Drive Sync", "Synchronisierungsfehler – wird erneut versucht")
		}

		interval := time.Duration(m.cfg.MirrorIntervalSec) * time.Second
		if interval < 30*time.Second {
			interval = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		case <-m.syncTrigger:
		}
	}
}

// -------- offline warming (stream mode) --------

func (m *Manager) warmOffline(ctx context.Context) {
	for _, p := range append([]string{}, m.cfg.OfflinePaths...) {
		if ctx.Err() != nil {
			return
		}
		m.warmPath(ctx, p)
	}
}

// warmPath reads every file under a Drive-relative path through the mount so it
// gets stored in the VFS cache and stays available offline.
func (m *Manager) warmPath(ctx context.Context, rel string) {
	root := filepath.Join(m.cfg.LocalDir, rel)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		_, _ = io.Copy(io.Discard, f)
		_ = f.Close()
		return nil
	})
}

func (m *Manager) forgetPath(ctx context.Context, rel string) {
	_ = m.ctl.Forget(ctx, rel)
}

// -------- helpers --------

func (m *Manager) pollStats(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		s, err := m.ctl.CoreStats(cctx)
		cancel()
		if err != nil {
			continue
		}
		m.mu.Lock()
		m.status.Bytes = s.Bytes
		m.status.Speed = s.Speed
		m.status.Errors = s.Errors
		if m.status.State == StateIdle || m.status.State == StateSyncing {
			if s.Transferring > 0 {
				m.status.State = StateSyncing
				m.status.Message = "Synchronisiere…"
			} else {
				if m.status.State == StateSyncing {
					m.status.LastSync = time.Now()
				}
				m.status.State = StateIdle
				m.status.Message = "Auf dem neuesten Stand"
			}
		}
		m.mu.Unlock()
		m.broadcast()
	}
}

// waitMountReady polls the mount's control server until it answers or timeout.
func (m *Manager) waitMountReady(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ok := m.ctl.Ping(cctx)
		cancel()
		if ok {
			return true
		}
		if sleepCtx(ctx, time.Second) {
			return false
		}
	}
	return false
}

// startProc launches rclone with args, streaming its output to the log, and
// returns the command plus a channel that receives its exit error.
func (m *Manager) startProc(args []string, tag string) (*exec.Cmd, chan error) {
	cmd := exec.Command(m.rc.Bin(), args...)
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		cmd.Stderr = cmd.Stdout
	}
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		done <- err
		return cmd, done
	}
	if stdout != nil {
		go func() {
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				m.logf("[%s] %s", tag, sc.Text())
			}
		}()
	}
	go func() { done <- cmd.Wait() }()
	return cmd, done
}

func (m *Manager) terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	go func() {
		time.Sleep(8 * time.Second)
		_ = cmd.Process.Kill()
	}()
}

func (m *Manager) unmount(mp string) {
	for _, tool := range []string{"fusermount3", "fusermount"} {
		if _, err := exec.LookPath(tool); err == nil {
			_ = exec.Command(tool, "-uz", mp).Run()
			return
		}
	}
}

func ensureDir(p string) error {
	return os.MkdirAll(p, 0o755)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func sanitize(p string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return r.Replace(strings.TrimPrefix(p, "/"))
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}
