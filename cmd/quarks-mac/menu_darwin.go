package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static NSMenu *submenu(NSMenu *bar, NSString *title) {
	NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:title action:nil keyEquivalent:@""];
	NSMenu *menu = [[NSMenu alloc] initWithTitle:title];
	item.submenu = menu;
	[bar addItem:item];
	return menu;
}

// webview sets up no menu bar, and without one macOS has nothing to route
// ⌘Q, ⌘C/⌘V (pasting a key in Settings) or ⌘W to.
static void quarksSetUpMenus(void) {
	NSMenu *bar = [[NSMenu alloc] init];

	NSMenu *app = submenu(bar, @"Quark's");
	[app addItemWithTitle:@"Masquer Quark's" action:@selector(hide:) keyEquivalent:@"h"];
	[app addItem:[NSMenuItem separatorItem]];
	[app addItemWithTitle:@"Quitter Quark's" action:@selector(terminate:) keyEquivalent:@"q"];

	NSMenu *edit = submenu(bar, @"Édition");
	[edit addItemWithTitle:@"Annuler" action:@selector(undo:) keyEquivalent:@"z"];
	[edit addItemWithTitle:@"Rétablir" action:@selector(redo:) keyEquivalent:@"Z"];
	[edit addItem:[NSMenuItem separatorItem]];
	[edit addItemWithTitle:@"Couper" action:@selector(cut:) keyEquivalent:@"x"];
	[edit addItemWithTitle:@"Copier" action:@selector(copy:) keyEquivalent:@"c"];
	[edit addItemWithTitle:@"Coller" action:@selector(paste:) keyEquivalent:@"v"];
	[edit addItemWithTitle:@"Tout sélectionner" action:@selector(selectAll:) keyEquivalent:@"a"];

	NSMenu *win = submenu(bar, @"Fenêtre");
	[win addItemWithTitle:@"Réduire" action:@selector(performMiniaturize:) keyEquivalent:@"m"];
	[win addItemWithTitle:@"Fermer" action:@selector(performClose:) keyEquivalent:@"w"];

	[NSApp setMainMenu:bar];
	[NSApp setWindowsMenu:win];
}
*/
import "C"

// setUpMenus installs the menu bar. Call it on the main thread, after the
// webview has created the shared NSApplication.
func setUpMenus() { C.quarksSetUpMenus() }
