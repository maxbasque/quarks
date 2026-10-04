// The two GTK calls the Linux window needs beyond webview's own API, made
// straight into the system's GLib and GTK through FFI.

const enc = new TextEncoder();
const cstr = (s: string) => enc.encode(s + "\0");

// setProgramName sets the GLib program name, which becomes the Wayland app_id
// / X11 WM_CLASS — how KDE matches the window to quarks.desktop for its name
// and icon. Call it before the window is created.
export function setProgramName(name: string) {
  try {
    const glib = Deno.dlopen("libglib-2.0.so.0", { g_set_prgname: { parameters: ["buffer"], result: "void" } });
    glib.symbols.g_set_prgname(cstr(name));
  } catch {
    // cosmetic: without it the taskbar shows a generic icon
  }
}

// setIconName sets the window's themed icon. libwebview is built against
// GTK 4, so that's the library to use; GTK 3 is only a fallback for a
// libwebview built the old way.
export function setIconName(window: Deno.PointerValue, icon: string) {
  if (!window) return;
  for (const lib of ["libgtk-4.so.1", "libgtk-3.so.0"]) {
    try {
      const gtk = Deno.dlopen(lib, {
        gtk_window_set_icon_name: { parameters: ["pointer", "buffer"], result: "void" },
      });
      gtk.symbols.gtk_window_set_icon_name(window, cstr(icon));
      return;
    } catch {
      continue;
    }
  }
}
