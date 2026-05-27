#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#include "splash_darwin.h"

#define WINDOW_W 340
#define WINDOW_H 280
#define CORNER_RADIUS 16.0
#define LOGO_SIZE 80.0
#define SPINNER_SIZE 28.0

static NSWindow *splashWindow = nil;
static NSTextField *statusLabel = nil;
static NSProgressIndicator *progressBar = nil;
static NSView *spinnerContainer = nil;
static BOOL appInitialized = NO;
static NSData *iconData = nil;
static NSString *appTitle = @"Application";
static CGFloat accentR = 0.0, accentG = 0.478, accentB = 1.0;

static NSColor* accentColor(void) {
    return [NSColor colorWithRed:accentR green:accentG blue:accentB alpha:1.0];
}

static void pumpEvents(void) {
    @autoreleasepool {
        NSEvent *event;
        while ((event = [NSApp nextEventMatchingMask:NSEventMaskAny
                                           untilDate:nil
                                              inMode:NSDefaultRunLoopMode
                                             dequeue:YES])) {
            [NSApp sendEvent:event];
        }
    }
}

static void ensureApp(void) {
    if (appInitialized) return;
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    appInitialized = YES;
}

void SplashSetIcon(const char *data, int length) {
    iconData = [NSData dataWithBytes:data length:length];
}

void SplashSetTitle(const char *title) {
    appTitle = [NSString stringWithUTF8String:title];
}

void SplashSetColor(float r, float g, float b) {
    accentR = r;
    accentG = g;
    accentB = b;
}

void SplashShow(const char *status) {
    if (splashWindow) {
        // Already shown: repurpose the window for the new phase rather than
        // ignoring the call, matching the Windows behaviour.
        [statusLabel setStringValue:[NSString stringWithUTF8String:status]];
        [progressBar setDoubleValue:0.0];
        [progressBar setHidden:YES];
        [CATransaction flush];
        pumpEvents();
        return;
    }
    ensureApp();

    // ── Window: borderless, rounded, floating ──
    NSRect frame = NSMakeRect(0, 0, WINDOW_W, WINDOW_H);
    splashWindow = [[NSWindow alloc] initWithContentRect:frame
                                               styleMask:NSWindowStyleMaskBorderless
                                                 backing:NSBackingStoreBuffered
                                                   defer:NO];
    [splashWindow setOpaque:NO];
    [splashWindow setBackgroundColor:[NSColor clearColor]];
    [splashWindow setHasShadow:YES];
    [splashWindow setLevel:NSFloatingWindowLevel];
    [splashWindow center];

    // ── Background: vibrancy for native look ──
    NSVisualEffectView *bg = [[NSVisualEffectView alloc] initWithFrame:frame];
    bg.material = NSVisualEffectMaterialPopover;
    bg.blendingMode = NSVisualEffectBlendingModeBehindWindow;
    bg.state = NSVisualEffectStateActive;
    bg.wantsLayer = YES;
    bg.layer.cornerRadius = CORNER_RADIUS;
    bg.layer.masksToBounds = YES;
    [splashWindow setContentView:bg];

    // ── Logo ──
    if (iconData) {
        NSImage *icon = [[NSImage alloc] initWithData:iconData];
        CGFloat logoX = (WINDOW_W - LOGO_SIZE) / 2;
        NSImageView *logoView = [[NSImageView alloc] initWithFrame:
            NSMakeRect(logoX, WINDOW_H - 30 - LOGO_SIZE, LOGO_SIZE, LOGO_SIZE)];
        [logoView setImage:icon];
        [logoView setImageScaling:NSImageScaleProportionallyUpOrDown];
        logoView.wantsLayer = YES;
        logoView.layer.cornerRadius = 14;
        logoView.layer.masksToBounds = YES;
        [bg addSubview:logoView];
    }

    // ── Title ──
    NSTextField *titleLabel = [[NSTextField alloc] initWithFrame:
        NSMakeRect(20, WINDOW_H - 30 - LOGO_SIZE - 8 - 26, WINDOW_W - 40, 26)];
    [titleLabel setStringValue:appTitle];
    [titleLabel setBezeled:NO];
    [titleLabel setDrawsBackground:NO];
    [titleLabel setEditable:NO];
    [titleLabel setSelectable:NO];
    [titleLabel setAlignment:NSTextAlignmentCenter];
    [titleLabel setFont:[NSFont systemFontOfSize:17 weight:NSFontWeightSemibold]];
    [bg addSubview:titleLabel];

    // ── Spinner: Core Animation arc ──
    CGFloat spinX = (WINDOW_W - SPINNER_SIZE) / 2;
    CGFloat spinY = titleLabel.frame.origin.y - 20 - SPINNER_SIZE;
    spinnerContainer = [[NSView alloc] initWithFrame:
        NSMakeRect(spinX, spinY, SPINNER_SIZE, SPINNER_SIZE)];
    spinnerContainer.wantsLayer = YES;

    CAShapeLayer *arc = [CAShapeLayer layer];
    arc.frame = CGRectMake(0, 0, SPINNER_SIZE, SPINNER_SIZE);
    CGFloat radius = (SPINNER_SIZE - 3) / 2;
    CGFloat center = SPINNER_SIZE / 2;
    CGMutablePathRef path = CGPathCreateMutable();
    CGPathAddArc(path, NULL, center, center, radius, 0, 1.5 * M_PI, NO);
    arc.path = path;
    arc.fillColor = nil;
    arc.strokeColor = [accentColor() CGColor];
    arc.lineWidth = 2.5;
    arc.lineCap = kCALineCapRound;
    CGPathRelease(path);

    CABasicAnimation *spin = [CABasicAnimation animationWithKeyPath:@"transform.rotation.z"];
    spin.fromValue = @0;
    spin.toValue = @(-2 * M_PI);
    spin.duration = 0.9;
    spin.repeatCount = HUGE_VALF;
    spin.timingFunction = [CAMediaTimingFunction functionWithName:kCAMediaTimingFunctionLinear];
    [arc addAnimation:spin forKey:@"spin"];

    [spinnerContainer.layer addSublayer:arc];
    [bg addSubview:spinnerContainer];

    // ── Progress bar (hidden; shown during downloads) ──
    CGFloat barY = spinY - 18;
    progressBar = [[NSProgressIndicator alloc] initWithFrame:
        NSMakeRect(50, barY, WINDOW_W - 100, 4)];
    [progressBar setStyle:NSProgressIndicatorStyleBar];
    [progressBar setIndeterminate:NO];
    [progressBar setMinValue:0.0];
    [progressBar setMaxValue:100.0];
    [progressBar setDoubleValue:0.0];
    [progressBar setHidden:YES];
    progressBar.wantsLayer = YES;
    progressBar.layer.cornerRadius = 2;
    [bg addSubview:progressBar];

    // ── Status text ──
    statusLabel = [[NSTextField alloc] initWithFrame:
        NSMakeRect(20, 30, WINDOW_W - 40, 20)];
    [statusLabel setStringValue:[NSString stringWithUTF8String:status]];
    [statusLabel setBezeled:NO];
    [statusLabel setDrawsBackground:NO];
    [statusLabel setEditable:NO];
    [statusLabel setSelectable:NO];
    [statusLabel setAlignment:NSTextAlignmentCenter];
    [statusLabel setFont:[NSFont systemFontOfSize:12]];
    [statusLabel setTextColor:[NSColor secondaryLabelColor]];
    [bg addSubview:statusLabel];

    // ── Present ──
    [splashWindow makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [CATransaction flush];
    pumpEvents();
}

void SplashUpdate(double percent, const char *status) {
    if (!splashWindow) return;
    if (percent > 0) {
        [progressBar setHidden:NO];
        [progressBar setDoubleValue:percent];
    }
    [statusLabel setStringValue:[NSString stringWithUTF8String:status]];
    [CATransaction flush];
    pumpEvents();
}

void SplashHide(void) {
    if (!splashWindow) return;
    [splashWindow orderOut:nil];
    splashWindow = nil;
    statusLabel = nil;
    progressBar = nil;
    spinnerContainer = nil;
    pumpEvents();
}

void SplashError(const char *message) {
    ensureApp();
    SplashHide();
    [NSApp activateIgnoringOtherApps:YES];
    NSAlert *alert = [[NSAlert alloc] init];
    [alert setMessageText:appTitle];
    [alert setInformativeText:[NSString stringWithUTF8String:message]];
    [alert setAlertStyle:NSAlertStyleCritical];
    [alert addButtonWithTitle:@"OK"];
    [alert runModal];
}
