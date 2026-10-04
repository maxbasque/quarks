//go:build linux && quarkswindow

// Command quarks-window is the Linux dashboard window: a native WebKitGTK
// window (through webview) on the already-running quarks server. It is only
// a viewer — the server stays the systemd --user service, and closing the
// window just closes the window. It replaces the Chromium --app window, so
// no Chromium-family browser is needed.
//
// Build it with `make window` (needs the WebKitGTK headers; see the Makefile).
package main

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

// The program name becomes the Wayland app_id / X11 WM_CLASS, which is how
// KDE matches the window to quarks.desktop for its name and icon.
static void quarks_set_prgname(void) { g_set_prgname("quarks"); }

static void quarks_set_icon(void *window) {
	gtk_window_set_icon_name(GTK_WINDOW(window), "quarks");
}
*/
import "C"

import (
	"flag"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	webview "github.com/webview/webview_go"
)

// GTK wants the UI on the process's main thread.
func init() { runtime.LockOSThread() }

// pageScript runs in every page the window loads. The window can't open new
// windows, so links meant for one (articles, "ouvrir l'original") and links
// to other sites go to the default browser through the server's /open.
// Ctrl+W / Ctrl+Q close the window, as in a browser app window.
const pageScript = `
document.addEventListener("click", function (e) {
  var a = e.target.closest && e.target.closest("a[href]");
  if (!a || !/^https?:/i.test(a.href)) return;
  if (a.target !== "_blank" && a.origin === location.origin) return;
  e.preventDefault();
  e.stopImmediatePropagation();
  fetch("/open", { method: "POST", body: new URLSearchParams({ url: a.href }) });
}, true);
document.addEventListener("keydown", function (e) {
  if (e.ctrlKey && !e.altKey && (e.key === "w" || e.key === "q")) {
    e.preventDefault();
    window.quarksClose();
  }
});
`

const downPage = `<!doctype html><meta charset="utf-8">
<body style="font:16px system-ui;background:#1c1b1f;color:#e8e2da;display:grid;place-items:center;height:100vh;margin:0">
<div style="max-width:32em;text-align:center">
<h2>Le serveur Quark's ne répond pas</h2>
<p>Vérifiez-le avec <code>systemctl --user status quarks</code>, puis rouvrez cette fenêtre.</p>
</div>`

func main() {
	url := flag.String("url", "http://localhost:7373/", "dashboard address")
	flag.Parse()

	up := serverUp(*url)
	if !up {
		// the service may just not be running (stopped, or not started yet)
		_ = exec.Command("systemctl", "--user", "start", "quarks.service").Run()
		for i := 0; i < 25 && !up; i++ {
			time.Sleep(200 * time.Millisecond)
			up = serverUp(*url)
		}
	}

	C.quarks_set_prgname()
	w := webview.New(false)
	defer w.Destroy()
	C.quarks_set_icon(w.Window())
	w.SetTitle("Quark's")
	w.SetSize(1500, 950, webview.HintNone)
	_ = w.Bind("quarksClose", func() { w.Terminate() })
	w.Init(pageScript)

	if up {
		w.Navigate(*url)
	} else {
		w.SetHtml(downPage)
	}
	w.Run()
}

func serverUp(url string) bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}
