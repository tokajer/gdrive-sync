# Google Drive Sync

Ein Google-Drive-Synchronisationsclient für Linux – funktional angelehnt an den
Windows-Client von Google Drive. Er läuft als Hintergrunddienst mit **Tray-Icon**,
bietet ein **natives Einstellungs-Fenster** (WebKitGTK, kein Browser) und wird als
**AppImage** ausgeliefert (eine Datei, keine Installation nötig).

Als Sync-Engine dient das bewährte [rclone](https://rclone.org), das im AppImage
mitgeliefert wird.

## Funktionen

- **Zwei Sync-Modi, jederzeit umschaltbar:**
  - **Stream (virtuelles Laufwerk):** Das gesamte Drive erscheint als Ordner,
    Dateien werden bei Bedarf geladen. Spart Speicherplatz.
  - **Mirror (lokale Kopie):** Vollständige Zwei-Wege-Synchronisation eines
    Ordners – alles offline verfügbar.
- **Offline verfügbar machen:** Im Stream-Modus lassen sich einzelne Ordner
  auswählen, die dauerhaft lokal vorgehalten und offline nutzbar bleiben –
  wie „Offline verfügbar machen" beim Windows-Client.
- **Tray-Icon** mit Statusfarbe (grün = aktuell, blau = synchronisiert,
  rot = Fehler) und Kontextmenü.
- **Natives Einstellungs-Fenster** mit Datei-Browser zum Auswählen der
  Offline-Ordner (nutzt das im System vorhandene WebKitGTK, kein Web-Browser).
- **Desktop-Benachrichtigungen** bei wichtigen Ereignissen.
- **Automatischer Neustart** des Mounts / erneuter Abgleich bei Verbindungs­abbruch.

## AppImage bauen

Voraussetzungen: Go ≥ 1.23, `curl`, `unzip`. rclone und appimagetool werden
automatisch heruntergeladen.

```bash
./build-appimage.sh
```

Ergebnis: `dist/Google_Drive_Sync-x86_64.AppImage`

Umgebungsvariablen (optional):

| Variable        | Zweck                                             |
|-----------------|---------------------------------------------------|
| `GO`            | Pfad zu einer bestimmten Go-Installation          |
| `RCLONE_BIN`    | vorhandene rclone-Binary statt Download verwenden |
| `APPIMAGETOOL`  | vorhandenes appimagetool verwenden                |

## Benutzung

```bash
chmod +x Google_Drive_Sync-x86_64.AppImage
./Google_Drive_Sync-x86_64.AppImage
```

Beim ersten Start öffnet sich das Einstellungs-Fenster. Dort auf
**„Bei Google anmelden"** klicken – für die Google-Anmeldung selbst öffnet sich
einmalig der Standardbrowser (OAuth). Danach Modus wählen und im Stream-Modus
optional Ordner als „offline" markieren.

Weitere Kommandos:

```bash
./Google_Drive_Sync-x86_64.AppImage login    # Anmeldung in der Konsole (headless)
./Google_Drive_Sync-x86_64.AppImage open     # Einstellungs-Fenster öffnen
./Google_Drive_Sync-x86_64.AppImage status   # Status ausgeben
```

Ein erneuter Start bei bereits laufendem Dienst öffnet einfach die Einstellungen.

### Autostart

Die AppImage einmal ausführen und im Dateimanager als Autostart eintragen, oder
das mitgelieferte Desktop-File (`packaging/gdrive-sync.desktop`) nach
`~/.config/autostart/` kopieren und `Exec=` auf den AppImage-Pfad anpassen.

## Voraussetzungen zur Laufzeit

- **FUSE 3** (`fusermount3`) für den Stream-Modus – auf den meisten Distributionen
  vorinstalliert.
- **WebKitGTK** (`libwebkit2gtk-4.1`) für das Einstellungs-Fenster – auf den
  meisten Desktops vorhanden. Fehlt es, läuft der Dienst weiter; das Fenster kann
  dann nicht geöffnet werden (die HTTP-Steuerung auf 127.0.0.1 bleibt aber aktiv).
- **Tray-Icon:** Es wird ein *StatusNotifierItem*-Host benötigt. KDE Plasma, XFCE,
  Cinnamon u. a. bringen das mit. Unter **GNOME** ist die Erweiterung
  *AppIndicator and KStatusNotifierItem Support* nötig. Ohne Tray-Host läuft der
  Dienst trotzdem – das Einstellungs-Fenster erreichst du dann über
  `gdrive-sync open`.

## Eigene Google-OAuth-Zugangsdaten (später)

Standardmäßig werden die in rclone eingebauten Google-Zugangsdaten verwendet –
sofort startklar für den privaten Gebrauch. Wenn das Projekt wächst und höhere
Limits / eigenes Branding gewünscht sind, genügt es, in der Konfigurationsdatei
`~/.config/gdrive-sync/config.yaml` die eigenen Werte einzutragen:

```yaml
google:
  client_id: "DEINE_CLIENT_ID"
  client_secret: "DEIN_CLIENT_SECRET"
```

Danach einmal neu anmelden. Eine eigene Client-ID legt man in der
[Google Cloud Console](https://console.cloud.google.com) an (OAuth-Client,
Typ „Desktop", Drive-API aktivieren).

## Konfiguration

Alle Einstellungen liegen unter `~/.config/gdrive-sync/`:

- `config.yaml` – App-Einstellungen (Modus, Ordner, Offline-Pfade, Port, OAuth)
- `rclone.conf` – rclone-Remote inkl. OAuth-Token

Der VFS-Cache (Stream-Modus) liegt unter `~/.cache/gdrive-sync/`.

## Architektur

```
cmd/gdrive-sync      Einstiegspunkt (Daemon, CLI, Single-Instance)
internal/config      Laden/Speichern der YAML-Konfiguration
internal/rclone      rclone-Wrapper (Login, mount, bisync, RC-API, Listing)
internal/manager     Sync-Manager: Modus-Steuerung, Status, Offline-Pinning
internal/webui       lokaler Steuer-Server (127.0.0.1) + eingebettete Oberfläche
internal/window      natives Einstellungs-Fenster (WebKitGTK via dlopen, ohne Dev-Header)
internal/tray        Tray-Icon über DBus StatusNotifierItem (ohne GTK/cgo)
internal/notify      Desktop-Benachrichtigungen über DBus
packaging/           AppRun, Desktop-File, Icon
build-appimage.sh    Build-Skript
```

**Sync-Modi im Detail:**

- *Stream* startet `rclone mount` mit vollem VFS-Cache. Als „offline" markierte
  Ordner werden vollständig durch den Mount gelesen, damit sie im Cache landen und
  ohne Verbindung verfügbar bleiben.
- *Mirror* nutzt `rclone bisync` für eine echte Zwei-Wege-Synchronisation und
  gleicht in einem einstellbaren Intervall (Standard 5 min) sowie auf Knopfdruck ab.

## Hinweise / bekannte Grenzen

- **Offline-Pinning (Stream):** Garantiert offline verfügbar sind gepinnte Ordner,
  solange der VFS-Cache nicht manuell geleert wird. Wer *alles* garantiert offline
  will, nutzt den **Mirror-Modus**. Die Umschaltung ist jederzeit möglich.
- Beim **ersten** Mirror-Abgleich führt rclone einen vollständigen `--resync` durch;
  das kann bei großen Drives dauern.
- Google-Docs/Sheets/Slides erscheinen (wie bei rclone üblich) als Verknüpfungs­dateien.

## Lizenz

Der App-Code steht unter der MIT-Lizenz. Das mitgelieferte rclone steht unter der
MIT-Lizenz (© Nick Craig-Wood).
