// Command quarks-mac is the macOS app: the dashboard server and a native
// window (WebKit, through webview) showing it, in one process. Opening the
// app starts both; quitting it, or closing its window, stops both — nothing
// keeps running in the background and nothing starts at login.
//
// It's built into Quark's.app by packaging/macos/build-app.sh. Linux keeps
// using cmd/quarks and a Chromium app window.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	webview "github.com/webview/webview_go"

	"github.com/maxbasque/quarks/internal/app"
)

// Cocoa wants the UI on the process's main thread.
func init() { runtime.LockOSThread() }

// preferredPort is the port the Linux build uses too; Spotify's redirect URI
// is registered against it. If something else holds it, any free port works.
const preferredPort = 7373

// linkScript runs in every page the window loads. The window can't open new
// windows, so links meant for one (articles, "ouvrir l'original") and links
// to other sites go to the default browser through the server's /open.
const linkScript = `
document.addEventListener("click", function (e) {
  var a = e.target.closest && e.target.closest("a[href]");
  if (!a || !/^https?:/i.test(a.href)) return;
  if (a.target !== "_blank" && a.origin === location.origin) return;
  e.preventDefault();
  e.stopImmediatePropagation();
  fetch("/open", { method: "POST", body: new URLSearchParams({ url: a.href }) });
}, true);
`

// errQuit is the user choosing Quitter in the broken-config dialog.
var errQuit = errors.New("quit")

func main() {
	err := run()
	if errors.Is(err, errQuit) {
		return
	}
	if err != nil {
		alert("Quark's n’a pas pu démarrer.\n\n" + err.Error())
		os.Exit(1)
	}
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(logWriter(home), &slog.HandlerOptions{Level: slog.LevelInfo}))

	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "-psn_") { // older Finder launches pass a process serial number
		os.Args = os.Args[:1]
	}

	cfgPath := filepath.Join(home, "Library", "Application Support", "quarks", "config.yaml")
	if err := seedConfig(cfgPath); err != nil {
		return err
	}
	cacheDir := filepath.Join(home, "Library", "Caches", "quarks")

	ln, err := listen()
	if err != nil {
		return err
	}
	a, err := app.New(cfgPath, cacheDir, log)
	if err != nil {
		ln.Close()
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	headless := os.Getenv("QUARKS_HEADLESS") != "" // CI: no window, no dialogs

	// A config that won't load — usually one left by an older version — gets
	// a choice instead of a dead end: start over from the default config
	// (the old file is kept beside it) or quit.
	for {
		err := a.Start(ctx)
		if err == nil {
			break
		}
		if headless || !errors.Is(err, app.ErrInitialConfig) {
			ln.Close()
			return err
		}
		if !askReset(err) {
			ln.Close()
			return errQuit
		}
		backup := cfgPath + ".ancien-" + time.Now().Format("2006-01-02-150405")
		if err := os.Rename(cfgPath, backup); err != nil {
			ln.Close()
			return err
		}
		log.Info("config reset", "backup", backup)
		if err := seedConfig(cfgPath); err != nil {
			ln.Close()
			return err
		}
	}

	served := make(chan error, 1)
	go func() { served <- a.Serve(ctx, ln) }()

	url := "http://127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port) + "/"

	// For CI: serve without a window until SIGTERM.
	if headless {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-sig:
		case err := <-served:
			return err
		}
		cancel()
		return nil
	}

	w := webview.New(false)
	defer w.Destroy()
	setUpMenus()
	w.SetTitle("Quark's")
	w.SetSize(1280, 800, webview.HintNone)
	w.Init(linkScript)

	// a server that can't start (a broken config.yaml) closes the window and
	// is reported once the run loop returns
	failed := make(chan error, 1)
	go func() {
		if err := <-served; err != nil {
			failed <- err
			w.Dispatch(w.Terminate)
		}
	}()

	w.Navigate(url)
	w.Run() // until the window closes

	cancel()
	time.Sleep(200 * time.Millisecond) // let in-flight cache writes land
	select {
	case err := <-failed:
		return err
	default:
		return nil
	}
}

// listen claims the preferred port, or any free one if it's taken.
func listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(preferredPort))
	if err == nil {
		return ln, nil
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

// seedConfig copies the default config out of the app bundle on first run.
func seedConfig(cfgPath string) error {
	if _, err := os.Stat(cfgPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	src := filepath.Join(filepath.Dir(exe), "..", "Resources", "config.example.yaml")
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("default config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cfgPath, data, 0o644)
}

// logWriter appends to ~/Library/Logs/quarks.log (Console.app shows it), or
// discards logs if that can't be opened — a Finder-launched app has no
// terminal to write to.
func logWriter(home string) io.Writer {
	dir := filepath.Join(home, "Library", "Logs")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "quarks.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return io.Discard
	}
	return f
}

// askReset explains a config error and asks whether to start over from the
// default config. True means Réinitialiser.
func askReset(cause error) bool {
	msg := "La configuration de Quark's contient une erreur :\n\n" + cause.Error() +
		"\n\nRéinitialiser remet la configuration par défaut. L’ancienne est gardée à côté, " +
		"dans Application Support › quarks."
	out, err := exec.Command("/usr/bin/osascript", "-e",
		`display dialog "`+osaQuote(msg)+`" with title "Quark's" buttons {"Quitter", "Réinitialiser"} `+
			`default button "Réinitialiser" cancel button "Quitter" with icon caution`).Output()
	return err == nil && strings.Contains(string(out), "Réinitialiser")
}

func osaQuote(s string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) }

// alert shows a plain dialog. It goes through osascript so it works even
// when the error came before (or instead of) the window.
func alert(msg string) {
	if os.Getenv("QUARKS_HEADLESS") != "" {
		fmt.Fprintln(os.Stderr, msg)
		return
	}
	_ = exec.Command("/usr/bin/osascript", "-e",
		`display dialog "`+osaQuote(msg)+`" with title "Quark's" buttons {"OK"} default button 1 with icon caution`).Run()
}
