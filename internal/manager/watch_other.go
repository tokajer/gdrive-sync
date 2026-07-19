//go:build !linux

package manager

import "context"

// watchMirror is a no-op on platforms without inotify; mirror mode then relies
// on interval-based polling alone.
func (m *Manager) watchMirror(ctx context.Context, root string) {}
