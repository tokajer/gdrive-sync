// Command gdrive-sync is a Google Drive sync client with a tray icon and a local
// settings UI, modelled on the Windows Google Drive client. It uses a bundled
// rclone binary as its sync engine.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"gdrive-sync/internal/config"
	"gdrive-sync/internal/logbuf"
	"gdrive-sync/internal/manager"
	"gdrive-sync/internal/notify"
	"gdrive-sync/internal/tray"
	"gdrive-sync/internal/webui"
)

const version = "0.1.0"

func main() {
	log.SetFlags(log.Ltime)
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "run", "":
		runDaemon()
	case "login":
		cliLogin()
	case "open", "ui":
		openSettings()
	case "status":
		cliStatus()
	case "version", "--version", "-v":
		fmt.Println("gdrive-sync", version)
	default:
		usage()
	}
}

func usage() {
	fmt.Print(`gdrive-sync – Google-Drive-Synchronisation

Verwendung:
  gdrive-sync [run]     Daemon mit Tray-Icon und Einstellungs-UI starten (Standard)
  gdrive-sync login     Google-Konto in der Konsole verbinden (headless)
  gdrive-sync open      Einstellungs-UI im Browser öffnen
  gdrive-sync status    Aktuellen Status anzeigen
  gdrive-sync version   Version anzeigen
`)
}

func loadOrExit() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Konfiguration konnte nicht geladen werden: %v", err)
	}
	return cfg
}

// runDaemon starts the sync backend, the settings web server and the tray icon.
func runDaemon() {
	cfg := loadOrExit()

	// Single-instance: if a daemon already answers on the web port, just open
	// its settings UI and exit (mimics clicking the app icon again).
	if instanceRunning(cfg.WebPort) {
		log.Println("Läuft bereits – öffne Einstellungen.")
		openURL(fmt.Sprintf("http://127.0.0.1:%d", cfg.WebPort))
		return
	}

	logs := logbuf.New(1000)
	logf := logs.Logf
	notifier := notify.NewDBus("Google Drive Sync", "gdrive-sync")

	mgr, err := manager.New(cfg, notifier, logf)
	if err != nil {
		log.Fatalf("Start fehlgeschlagen: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	mgr.Start(ctx)

	web := webui.New(mgr, cfg, logs)

	// Tray icon (best-effort; the daemon runs fine without it).
	go func() {
		act := tray.Actions{
			OpenFolder:   func() { openURL(cfg.LocalDir) },
			SyncNow:      func() { mgr.SyncNow() },
			TogglePause:  func() { togglePause(mgr) },
			OpenSettings: func() { openURL(web.URL()) },
			Logout: func() {
				c, cl := context.WithTimeout(context.Background(), 30*time.Second)
				defer cl()
				_ = mgr.Logout(c)
			},
			Quit: cancel,
		}
		if err := tray.Run(ctx, mgr, act, logf); err != nil {
			log.Printf("Kein Tray-Icon: %v (Daemon läuft weiter, Steuerung über %s)", err, web.URL())
		}
	}()

	// On first launch, open the settings UI so the user can sign in.
	if !cfg.Configured() {
		log.Printf("Noch nicht angemeldet – öffne %s", web.URL())
		go func() { time.Sleep(500 * time.Millisecond); openURL(web.URL()) }()
	} else {
		log.Printf("Einstellungen: %s", web.URL())
	}

	if err := web.ListenAndServe(ctx); err != nil {
		log.Printf("Web-UI-Fehler: %v", err)
	}
	mgr.Shutdown()
	log.Println("Beendet.")
}

func togglePause(mgr *manager.Manager) {
	if mgr.Status().State == manager.StatePaused {
		mgr.Resume()
	} else {
		mgr.Pause()
	}
}

// cliLogin runs the OAuth flow in the terminal (for headless setups).
func cliLogin() {
	cfg := loadOrExit()
	mgr, err := manager.New(cfg, notify.Noop{}, func(f string, a ...any) {})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Starte Google-Anmeldung – folge dem Link im Browser…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := mgr.Login(ctx, func(line string) { fmt.Println(line) }); err != nil {
		log.Fatalf("Anmeldung fehlgeschlagen: %v", err)
	}
	fmt.Println("Angemeldet als", cfg.AccountEmail)
}

func openSettings() {
	cfg := loadOrExit()
	openURL(fmt.Sprintf("http://127.0.0.1:%d", cfg.WebPort))
}

func cliStatus() {
	cfg := loadOrExit()
	if !instanceRunning(cfg.WebPort) {
		fmt.Println("Daemon läuft nicht.")
		return
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", cfg.WebPort))
	if err != nil {
		fmt.Println("Status nicht abrufbar:", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func instanceRunning(port int) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", port))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

func openURL(target string) {
	if err := exec.Command("xdg-open", target).Start(); err != nil {
		log.Printf("xdg-open fehlgeschlagen: %v", err)
	}
}
