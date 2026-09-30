//go:build darwin && !nowebview

package window

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// DragStrip is a transparent strip over the top of the web view that moves
// the window (and zooms it on double-click), since a web view swallows the
// mouse events a native title bar would get.
@interface Pad2GoDragStrip : NSView
@end
@implementation Pad2GoDragStrip
- (BOOL)mouseDownCanMoveWindow { return YES; }
- (void)mouseDown:(NSEvent *)event {
	if (event.clickCount == 2) { [self.window performZoom:nil]; return; }
	[self.window performWindowDragWithEvent:event];
}
@end

// nativeChrome gives the window a macOS look: the web view fills the whole
// window, including under a transparent title bar, over a translucent
// sidebar material that shows through its transparent background.
static void nativeChrome(void *handle) {
	NSWindow *window = (__bridge NSWindow *)handle;
	NSView *web = window.contentView;
	if (web == nil) return;

	window.titlebarAppearsTransparent = YES;
	window.titleVisibility = NSWindowTitleHidden;
	window.styleMask |= NSWindowStyleMaskFullSizeContentView;
	// An empty unified toolbar gives the 52 pt title bar of System Settings,
	// with the traffic lights centred in it; the page draws the title.
	NSToolbar *toolbar = [[NSToolbar alloc] initWithIdentifier:@"pad2go"];
	window.toolbar = toolbar;
	if (@available(macOS 11.0, *)) {
		window.toolbarStyle = NSWindowToolbarStyleUnified;
		window.titlebarSeparatorStyle = NSTitlebarSeparatorStyleNone;
	}
	window.minSize = NSMakeSize(760, 540);
	// A window with transparent regions lets clicks there fall through to
	// whatever is behind it by default; the sidebar is such a region.
	window.ignoresMouseEvents = NO;

	NSVisualEffectView *fx = [[NSVisualEffectView alloc] initWithFrame:web.frame];
	fx.material = NSVisualEffectMaterialSidebar;
	fx.blendingMode = NSVisualEffectBlendingModeBehindWindow;
	fx.state = NSVisualEffectStateFollowsWindowActiveState;
	window.contentView = fx;

	web.frame = fx.bounds;
	web.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
	[fx addSubview:web];
	if ([web isKindOfClass:[WKWebView class]]) {
		[web setValue:@NO forKey:@"drawsBackground"];
		if (@available(macOS 12.0, *)) {
			((WKWebView *)web).underPageBackgroundColor = NSColor.clearColor;
		}
	}

	// The page keeps this top band free of controls (see styles.css).
	CGFloat stripHeight = 52;
	NSRect b = fx.bounds;
	Pad2GoDragStrip *strip = [[Pad2GoDragStrip alloc] initWithFrame:NSMakeRect(0, b.size.height - stripHeight, b.size.width, stripHeight)];
	strip.autoresizingMask = NSViewWidthSizable | NSViewMinYMargin;
	[fx addSubview:strip positioned:NSWindowAbove relativeTo:web];
	[window center];
}
*/
import "C"

import "unsafe"

func nativeChrome(handle unsafe.Pointer) {
	if handle != nil {
		C.nativeChrome(handle)
	}
}
