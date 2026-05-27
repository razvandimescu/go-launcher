//go:build windows

// splash-ghosttest exercises the launcher's real splash sequence — two shows
// with no hide between them (a phase change), a hide, then a re-show — and
// reports how many GoLauncherSplash windows survive at each step.
//
// Default mode is a narrated, slow VISUAL test meant to be watched on the
// interactive desktop (run it locally or over AnyDesk/RDP, not plain SSH).
// The -headless flag runs it fast for automation: exit 0 = clean, 1 = ghost.
//
// buildLabel is injected at compile time: -ldflags "-X main.buildLabel=FIXED".
package main

import (
	"flag"
	"fmt"
	"os"
	"time"
	"unsafe"

	"github.com/razvandimescu/go-launcher/ui/splash"
	"golang.org/x/sys/windows"
)

var buildLabel = "DEV"

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	pFindWindowExW   = user32.NewProc("FindWindowExW")
	pIsWindowVisible = user32.NewProc("IsWindowVisible")
)

func countSplashWindows() (total, visible int) {
	class := windows.StringToUTF16Ptr("GoLauncherSplash")
	var prev uintptr
	for {
		h, _, _ := pFindWindowExW.Call(0, prev, uintptr(unsafe.Pointer(class)), 0)
		if h == 0 {
			break
		}
		total++
		if v, _, _ := pIsWindowVisible.Call(h); v != 0 {
			visible++
		}
		prev = h
	}
	return
}

func alive(note string) int {
	t, v := countSplashWindows()
	fmt.Printf("    [count] windows alive: %d (visible: %d)   %s\n", t, v, note)
	return t
}

func main() {
	headless := flag.Bool("headless", false, "fast, no pauses; exit 0=clean 1=ghost (for automation)")
	flag.Parse()
	if *headless {
		runHeadless()
		return
	}
	runWatch()
}

func runHeadless() {
	ui := splash.New(splash.Config{AppName: "Ghost Test", AccentHex: "#2E67B2"})
	ui.ShowSplash("Phase one...")
	time.Sleep(700 * time.Millisecond)
	ui.ShowSplash("Phase two...")
	time.Sleep(700 * time.Millisecond)
	ui.HideSplash()
	time.Sleep(1500 * time.Millisecond)
	if t, _ := countSplashWindows(); t > 0 {
		fmt.Printf("RESULT: GHOST (%d window(s) survived)\n", t)
		os.Exit(1)
	}
	fmt.Println("RESULT: CLEAN")
}

func runWatch() {
	fmt.Println("============================================================")
	fmt.Printf("  go-launcher splash VISUAL test        build: %s\n", buildLabel)
	fmt.Println("============================================================")
	fmt.Println("Watch the CENTER of the screen and follow the prompts.")
	fmt.Println("Starting in 3 seconds...")
	time.Sleep(3 * time.Second)

	ui := splash.New(splash.Config{AppName: "Visual Test", AccentHex: "#2E67B2"})

	fmt.Println("\n[STEP 1/5] Showing splash \"Setting up...\".")
	fmt.Println("    EXPECT: a small window (title + spinning ring + status) appears, centered.")
	ui.ShowSplash("Setting up...")
	time.Sleep(4 * time.Second)
	alive("(expect 1)")

	fmt.Println("\n[STEP 2/5] Showing splash AGAIN as \"Starting...\" WITHOUT hiding first.")
	fmt.Println("    FIXED build -> SAME window stays; text changes to \"Starting...\".")
	fmt.Println("    BUGGY build -> a SECOND window stacks on top.")
	ui.ShowSplash("Starting...")
	time.Sleep(4 * time.Second)
	alive("(FIXED=1, BUGGY=2)")

	fmt.Println("\n[STEP 3/5] Hiding the splash NOW.")
	fmt.Println("    *** LOOK AT SCREEN CENTER FOR 6 SECONDS ***")
	fmt.Println("    Move the mouse slowly across the center.")
	fmt.Println("    PASS: area is clear; normal arrow cursor.")
	fmt.Println("    FAIL: a faint see-through rounded box lingers with a shadow on its")
	fmt.Println("          lower edges, and the cursor CHANGES when hovering over it.")
	ui.HideSplash()
	time.Sleep(6 * time.Second)
	alive("(FIXED=0, BUGGY=1 <- the ghost)")

	fmt.Println("\n[STEP 4/5] Showing splash again as \"Restarting...\" (re-paint test).")
	fmt.Println("    PASS: a FULLY DRAWN splash reappears (title + spinner + status).")
	fmt.Println("    FAIL: a BLANK / see-through box appears.")
	ui.ShowSplash("Restarting...")
	time.Sleep(4 * time.Second)
	alive("(FIXED=1)")

	fmt.Println("\n[STEP 5/5] Final hide.")
	ui.HideSplash()
	time.Sleep(3 * time.Second)
	t := alive("(FIXED=0)")

	fmt.Println()
	if t > 0 {
		fmt.Printf(">>> RESULT: GHOST DETECTED — %d window still alive (this build is BAD)\n", t)
	} else {
		fmt.Println(">>> RESULT: CLEAN — no leftover windows.")
	}
	fmt.Print("\nPress Enter to close...")
	fmt.Scanln()
}
