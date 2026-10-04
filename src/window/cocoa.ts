// The macOS menu bar, built through the Objective-C runtime over FFI. webview
// sets up no menu bar, and without one macOS has nothing to route ⌘Q, ⌘C/⌘V
// (pasting a key in Settings) or ⌘W to.

const enc = new TextEncoder();
const cstr = (s: string) => enc.encode(s + "\0");

// objc_msgSend is one C symbol called with whatever arguments the selector
// takes; each shape gets its own binding.
function runtime() {
  Deno.dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", {});
  return Deno.dlopen(
    "/usr/lib/libobjc.A.dylib",
    {
      objc_getClass: { parameters: ["buffer"], result: "pointer" },
      sel_registerName: { parameters: ["buffer"], result: "pointer" },
      send0: { name: "objc_msgSend", parameters: ["pointer", "pointer"], result: "pointer" },
      send1: { name: "objc_msgSend", parameters: ["pointer", "pointer", "pointer"], result: "pointer" },
      sendStr: { name: "objc_msgSend", parameters: ["pointer", "pointer", "buffer"], result: "pointer" },
      send3: {
        name: "objc_msgSend",
        parameters: ["pointer", "pointer", "pointer", "pointer", "pointer"],
        result: "pointer",
      },
    } as const,
  );
}

// setUpMenus installs the menu bar. Call it on the main thread, after the
// webview has created the shared NSApplication.
export function setUpMenus() {
  const rt = runtime();
  const { objc_getClass, sel_registerName, send0, send1, sendStr, send3 } = rt.symbols;
  const cls = (name: string) => objc_getClass(cstr(name));
  const sel = (name: string) => sel_registerName(cstr(name));
  const str = (s: string) => sendStr(cls("NSString"), sel("stringWithUTF8String:"), cstr(s));
  const alloc = (c: string) => send0(cls(c), sel("alloc"));

  const bar = send0(alloc("NSMenu"), sel("init"));
  const submenu = (title: string) => {
    const item = send3(alloc("NSMenuItem"), sel("initWithTitle:action:keyEquivalent:"), str(title), null, str(""));
    const menu = send1(alloc("NSMenu"), sel("initWithTitle:"), str(title));
    send1(item, sel("setSubmenu:"), menu);
    send1(bar, sel("addItem:"), item);
    return menu;
  };
  const add = (menu: Deno.PointerValue, title: string, action: string, key: string) =>
    send3(menu, sel("addItemWithTitle:action:keyEquivalent:"), str(title), sel(action), str(key));
  const separator = (menu: Deno.PointerValue) =>
    send1(menu, sel("addItem:"), send0(cls("NSMenuItem"), sel("separatorItem")));

  const app = submenu("Quark's");
  add(app, "Masquer Quark's", "hide:", "h");
  separator(app);
  add(app, "Quitter Quark's", "terminate:", "q");

  const edit = submenu("Édition");
  add(edit, "Annuler", "undo:", "z");
  add(edit, "Rétablir", "redo:", "Z");
  separator(edit);
  add(edit, "Couper", "cut:", "x");
  add(edit, "Copier", "copy:", "c");
  add(edit, "Coller", "paste:", "v");
  add(edit, "Tout sélectionner", "selectAll:", "a");

  const win = submenu("Fenêtre");
  add(win, "Réduire", "performMiniaturize:", "m");
  add(win, "Fermer", "performClose:", "w");

  const nsApp = send0(cls("NSApplication"), sel("sharedApplication"));
  send1(nsApp, sel("setMainMenu:"), bar);
  send1(nsApp, sel("setWindowsMenu:"), win);
}
