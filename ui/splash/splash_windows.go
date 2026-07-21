//go:build windows

package splash

import (
	"math"
	"runtime"
	"sync"
	"time"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ── DLL / proc handles ──

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	gdiplus  = windows.NewLazySystemDLL("gdiplus.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")

	// user32
	pCreateWindowExW     = user32.NewProc("CreateWindowExW")
	pDefWindowProcW      = user32.NewProc("DefWindowProcW")
	pRegisterClassExW    = user32.NewProc("RegisterClassExW")
	pShowWindow          = user32.NewProc("ShowWindow")
	pDestroyWindow       = user32.NewProc("DestroyWindow")
	pGetMessage          = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessage     = user32.NewProc("DispatchMessageW")
	pPostMessage         = user32.NewProc("PostMessageW")
	pGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	pMessageBoxW         = user32.NewProc("MessageBoxW")
	pBeginPaint          = user32.NewProc("BeginPaint")
	pEndPaint            = user32.NewProc("EndPaint")
	pInvalidateRect      = user32.NewProc("InvalidateRect")
	pSetTimer            = user32.NewProc("SetTimer")
	pKillTimer           = user32.NewProc("KillTimer")
	pSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	pSetWindowRgn        = user32.NewProc("SetWindowRgn")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")

	// kernel32
	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	pGlobalLock       = kernel32.NewProc("GlobalLock")
	pGlobalUnlock     = kernel32.NewProc("GlobalUnlock")

	// gdi32
	pCreateRoundRectRgn     = gdi32.NewProc("CreateRoundRectRgn")
	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")

	// gdiplus
	pGdiplusStartup               = gdiplus.NewProc("GdiplusStartup")
	pGdiplusShutdown              = gdiplus.NewProc("GdiplusShutdown")
	pGdipCreateFromHDC            = gdiplus.NewProc("GdipCreateFromHDC")
	pGdipDeleteGraphics           = gdiplus.NewProc("GdipDeleteGraphics")
	pGdipSetSmoothingMode         = gdiplus.NewProc("GdipSetSmoothingMode")
	pGdipSetTextRenderingHint     = gdiplus.NewProc("GdipSetTextRenderingHint")
	pGdipLoadImageFromStream      = gdiplus.NewProc("GdipLoadImageFromStream")
	pGdipDisposeImage             = gdiplus.NewProc("GdipDisposeImage")
	pGdipDrawImageRectI           = gdiplus.NewProc("GdipDrawImageRectI")
	pGdipCreatePen1               = gdiplus.NewProc("GdipCreatePen1")
	pGdipDeletePen                = gdiplus.NewProc("GdipDeletePen")
	pGdipSetPenLineCap197819      = gdiplus.NewProc("GdipSetPenLineCap197819")
	pGdipDrawArc                  = gdiplus.NewProc("GdipDrawArc")
	pGdipCreateFontFamilyFromName = gdiplus.NewProc("GdipCreateFontFamilyFromName")
	pGdipDeleteFontFamily         = gdiplus.NewProc("GdipDeleteFontFamily")
	pGdipCreateFont               = gdiplus.NewProc("GdipCreateFont")
	pGdipDeleteFont               = gdiplus.NewProc("GdipDeleteFont")
	pGdipCreateSolidFill          = gdiplus.NewProc("GdipCreateSolidFill")
	pGdipDeleteBrush              = gdiplus.NewProc("GdipDeleteBrush")
	pGdipCreateStringFormat       = gdiplus.NewProc("GdipCreateStringFormat")
	pGdipDeleteStringFormat       = gdiplus.NewProc("GdipDeleteStringFormat")
	pGdipSetStringFormatAlign     = gdiplus.NewProc("GdipSetStringFormatAlign")
	pGdipDrawString               = gdiplus.NewProc("GdipDrawString")
	pGdipFillRectangleI           = gdiplus.NewProc("GdipFillRectangleI")
	pGdipTranslateWorldTransform  = gdiplus.NewProc("GdipTranslateWorldTransform")
	pGdipRotateWorldTransform     = gdiplus.NewProc("GdipRotateWorldTransform")
	pGdipResetWorldTransform      = gdiplus.NewProc("GdipResetWorldTransform")

	// ole32
	pCreateStreamOnHGlobal = ole32.NewProc("CreateStreamOnHGlobal")
)

// ── Win32 / GDI+ constants ──

const (
	wmDestroy = 0x0002
	wmPaint   = 0x000F
	wmTimer   = 0x0113
	wmEraseBG = 0x0014
	wmUser    = 0x0400

	wmApp       = wmUser + 100
	wmAppUpdate = wmApp + 1
	wmAppHide   = wmApp + 2
	wmAppError  = wmApp + 3

	wsPopup     = 0x80000000
	wsVisible   = 0x10000000
	wsExTopmost = 0x00000008
	wsExToolWin = 0x00000080

	csDropShadow = 0x00020000
	swHide       = 0
	swShow       = 5
	smCxscreen   = 0
	smCyscreen   = 1
	mbIconerror  = 0x00000010
	gmemMoveable = 0x0002

	smoothingAntiAlias     = 4
	textRenderingClearType = 5
	stringAlignCenter      = 1
	matrixOrderPrepend     = 0
	fontStyleBold          = 1
	fontStyleRegular       = 0
	unitPixel              = 2
	lineCapRound           = 2
	spinnerTimerID         = 1
	srcCopy                = 0x00CC0020
	spinnerPeriodMs        = 1000 // one full revolution per second
)

// ── Layout (matches macOS) ──

const (
	winW         = 340
	winH         = 280
	cornerRadius = 16
	logoSize     = 80
	spinnerSize  = 28
)

// ── Colors (ARGB for GDI+) ──

const (
	colorWhiteBg   = 0xFFFFFFFF
	colorLightGray = 0xFFE0E0E0
	colorTitle     = 0xFF1A1A1A
	colorStatus    = 0xFF888888
)

// ── Win32 structs ──

type wndClassExW struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type paintStruct struct {
	hdc         uintptr
	fErase      int32
	rcPaintL    int32
	rcPaintT    int32
	rcPaintR    int32
	rcPaintB    int32
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

type gdiplusStartupInput struct {
	GdiplusVersion           uint32
	DebugEventCallback       uintptr
	SuppressBackgroundThread int32
	SuppressExternalCodecs   int32
}

type rectF struct {
	X, Y, Width, Height float32
}

// ── Command types ──

const (
	cmdUpdate = iota
	cmdHide
	cmdError
)

type splashCmd struct {
	kind    int
	percent float64
	text    string
}

// ── winSplash ──

type winSplash struct {
	hwnd uintptr
	cmds chan splashCmd

	cfg        Config
	accentARGB uint32

	mu          sync.Mutex
	running     bool
	statusText  string
	progressPct float64

	// sendGuaranteedTimeout bounds sendGuaranteed's wait — overridable per
	// instance in tests to avoid a real multi-second sleep.
	sendGuaranteedTimeout time.Duration

	startTime time.Time

	gdipToken  uintptr
	gdipImage  uintptr
	titleFont  uintptr
	statusFont uintptr
	fontFamily uintptr
	strFormat  uintptr

	// cached GDI+ brushes and pens (reused across frames)
	whiteBrush     uintptr
	lightGrayBrush uintptr
	accentBrush    uintptr
	titleBrush     uintptr
	statusBrush    uintptr
	trackPen       uintptr
	arcPen         uintptr

	// double-buffer (lazy-created on first paint)
	memDC     uintptr
	memBitmap uintptr
	oldBitmap uintptr
}

func newPlatform(cfg Config) *winSplash {
	r, g, b := parseHexRGB(cfg.AccentHex)
	argb := uint32(0xFF000000) | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
	return &winSplash{
		cmds:                  make(chan splashCmd, 16),
		cfg:                   cfg,
		accentARGB:            argb,
		sendGuaranteedTimeout: 2 * time.Second,
	}
}

func (s *winSplash) ShowSplash(status string) {
	s.mu.Lock()
	s.statusText = status
	s.progressPct = 0
	if s.running {
		s.mu.Unlock()
		// A window is already up — repurpose it for the new phase rather
		// than spawning a second run() goroutine that would clobber the
		// shared GDI+ state and leak the existing window. Goes through the
		// same guaranteed-delivery path as HideSplash/ShowError instead of
		// posting directly to s.hwnd, so this can't race a concurrent
		// teardown the way a direct post to a dying window handle could
		// (the post would silently vanish, losing the new phase's text).
		s.sendGuaranteed(splashCmd{kind: cmdUpdate, text: status})
		return
	}
	s.running = true
	s.mu.Unlock()
	go s.run()
}

// UpdateProgress is intentionally lossy: it fires on every chunk of a
// download (potentially thousands of times), so dropping a frame under
// backpressure is harmless — the next one repaints correctly a moment
// later either way.
func (s *winSplash) UpdateProgress(percent float64, status string) {
	select {
	case s.cmds <- splashCmd{kind: cmdUpdate, percent: percent, text: status}:
	default:
	}
}

func (s *winSplash) HideSplash() {
	s.sendGuaranteed(splashCmd{kind: cmdHide})
}

func (s *winSplash) ShowError(msg string) {
	s.sendGuaranteed(splashCmd{kind: cmdError, text: msg})
}

func (s *winSplash) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// sendGuaranteed delivers a one-shot, must-not-drop command (hide, error, or
// a repurposed show) — unlike UpdateProgress, losing one of these leaves the
// splash stuck on screen (or hidden when it should reappear) with no other
// code path to retry it. A plain blocking send would be safe as long as
// dispatchCommands is alive and draining (true whenever running is set),
// but bounding it with a timeout means a caller can never hang forever even
// if that invariant is ever violated by a future change.
func (s *winSplash) sendGuaranteed(cmd splashCmd) {
	if !s.isRunning() {
		return
	}
	select {
	case s.cmds <- cmd:
	case <-time.After(s.sendGuaranteedTimeout):
	}
}

func f32bits(f float32) uintptr {
	return uintptr(math.Float32bits(f))
}

func gdipFillRect(graphics, brush uintptr, x, y, w, h int) {
	pGdipFillRectangleI.Call(graphics, brush, uintptr(x), uintptr(y), uintptr(w), uintptr(h))
}

func loadPNGFromMemory(data []byte) uintptr {
	hMem, _, _ := pGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if hMem == 0 {
		return 0
	}
	ptr, _, _ := pGlobalLock.Call(hMem)
	if ptr == 0 {
		return 0
	}
	copy((*[1 << 30]byte)(unsafe.Pointer(ptr))[:len(data)], data)
	pGlobalUnlock.Call(hMem)

	var stream uintptr
	pCreateStreamOnHGlobal.Call(hMem, 1, uintptr(unsafe.Pointer(&stream)))
	if stream == 0 {
		return 0
	}

	var img uintptr
	pGdipLoadImageFromStream.Call(stream, uintptr(unsafe.Pointer(&img)))
	return img
}

func (s *winSplash) initGDIPlus() {
	input := gdiplusStartupInput{GdiplusVersion: 1}
	pGdiplusStartup.Call(
		uintptr(unsafe.Pointer(&s.gdipToken)),
		uintptr(unsafe.Pointer(&input)),
		0,
	)

	if len(s.cfg.Logo) > 0 {
		s.gdipImage = loadPNGFromMemory(s.cfg.Logo)
	}

	familyName := windows.StringToUTF16Ptr("Segoe UI")
	pGdipCreateFontFamilyFromName.Call(
		uintptr(unsafe.Pointer(familyName)), 0,
		uintptr(unsafe.Pointer(&s.fontFamily)),
	)

	pGdipCreateFont.Call(s.fontFamily, f32bits(17.0), fontStyleBold, unitPixel,
		uintptr(unsafe.Pointer(&s.titleFont)))

	pGdipCreateFont.Call(s.fontFamily, f32bits(12.0), fontStyleRegular, unitPixel,
		uintptr(unsafe.Pointer(&s.statusFont)))

	pGdipCreateStringFormat.Call(0, 0, uintptr(unsafe.Pointer(&s.strFormat)))
	pGdipSetStringFormatAlign.Call(s.strFormat, stringAlignCenter)

	pGdipCreateSolidFill.Call(uintptr(uint32(colorWhiteBg)), uintptr(unsafe.Pointer(&s.whiteBrush)))
	pGdipCreateSolidFill.Call(uintptr(uint32(colorLightGray)), uintptr(unsafe.Pointer(&s.lightGrayBrush)))
	pGdipCreateSolidFill.Call(uintptr(s.accentARGB), uintptr(unsafe.Pointer(&s.accentBrush)))
	pGdipCreateSolidFill.Call(uintptr(uint32(colorTitle)), uintptr(unsafe.Pointer(&s.titleBrush)))
	pGdipCreateSolidFill.Call(uintptr(uint32(colorStatus)), uintptr(unsafe.Pointer(&s.statusBrush)))

	pGdipCreatePen1.Call(uintptr(uint32(colorLightGray)), f32bits(2.5), unitPixel,
		uintptr(unsafe.Pointer(&s.trackPen)))
	pGdipCreatePen1.Call(uintptr(s.accentARGB), f32bits(2.5), unitPixel,
		uintptr(unsafe.Pointer(&s.arcPen)))
	pGdipSetPenLineCap197819.Call(s.arcPen, lineCapRound, lineCapRound, 0)
}

func (s *winSplash) shutdownGDIPlus() {
	for _, b := range []uintptr{s.whiteBrush, s.lightGrayBrush, s.accentBrush, s.titleBrush, s.statusBrush} {
		if b != 0 {
			pGdipDeleteBrush.Call(b)
		}
	}
	if s.trackPen != 0 {
		pGdipDeletePen.Call(s.trackPen)
	}
	if s.arcPen != 0 {
		pGdipDeletePen.Call(s.arcPen)
	}
	if s.strFormat != 0 {
		pGdipDeleteStringFormat.Call(s.strFormat)
	}
	if s.titleFont != 0 {
		pGdipDeleteFont.Call(s.titleFont)
	}
	if s.statusFont != 0 {
		pGdipDeleteFont.Call(s.statusFont)
	}
	if s.fontFamily != 0 {
		pGdipDeleteFontFamily.Call(s.fontFamily)
	}
	if s.gdipImage != 0 {
		pGdipDisposeImage.Call(s.gdipImage)
	}
	if s.gdipToken != 0 {
		pGdiplusShutdown.Call(s.gdipToken)
	}
	if s.memDC != 0 {
		if s.oldBitmap != 0 {
			pSelectObject.Call(s.memDC, s.oldBitmap)
		}
		if s.memBitmap != 0 {
			pDeleteObject.Call(s.memBitmap)
		}
		pDeleteDC.Call(s.memDC)
		// Zero so a subsequent run() lazily recreates the buffer in paint()
		// instead of drawing into freed handles.
		s.memDC, s.memBitmap, s.oldBitmap = 0, 0, 0
	}
}

func (s *winSplash) paint(hdc uintptr) {
	if s.memDC == 0 {
		s.memDC, _, _ = pCreateCompatibleDC.Call(hdc)
		s.memBitmap, _, _ = pCreateCompatibleBitmap.Call(hdc, winW, winH)
		s.oldBitmap, _, _ = pSelectObject.Call(s.memDC, s.memBitmap)
	}

	var graphics uintptr
	pGdipCreateFromHDC.Call(s.memDC, uintptr(unsafe.Pointer(&graphics)))
	if graphics == 0 {
		return
	}

	pGdipSetSmoothingMode.Call(graphics, smoothingAntiAlias)
	pGdipSetTextRenderingHint.Call(graphics, textRenderingClearType)

	gdipFillRect(graphics, s.whiteBrush, 0, 0, winW, winH)

	if s.gdipImage != 0 {
		logoX := (winW - logoSize) / 2
		pGdipDrawImageRectI.Call(graphics, s.gdipImage,
			uintptr(logoX), 30, logoSize, logoSize)
	}

	titleY := float32(30 + logoSize + 8)
	s.drawCenteredText(graphics, s.cfg.AppName, s.titleFont, s.titleBrush, titleY, 26)

	s.drawSpinner(graphics)

	s.mu.Lock()
	pct := s.progressPct
	status := s.statusText
	s.mu.Unlock()

	if pct > 0 {
		barX, barY, barW, barH := 50, 204, winW-100, 4
		gdipFillRect(graphics, s.lightGrayBrush, barX, barY, barW, barH)
		fillW := int(float64(barW) * pct / 100.0)
		if fillW > 0 {
			gdipFillRect(graphics, s.accentBrush, barX, barY, fillW, barH)
		}
	}

	if status != "" {
		s.drawCenteredText(graphics, status, s.statusFont, s.statusBrush, 234, 20)
	}

	pGdipDeleteGraphics.Call(graphics)
	pBitBlt.Call(hdc, 0, 0, winW, winH, s.memDC, 0, 0, srcCopy)
}

func (s *winSplash) drawCenteredText(graphics uintptr, text string, font, brush uintptr, y, height float32) {
	textPtr := windows.StringToUTF16Ptr(text)
	textLen := utf8.RuneCountInString(text)

	rect := rectF{X: 0, Y: y, Width: winW, Height: height}
	pGdipDrawString.Call(graphics,
		uintptr(unsafe.Pointer(textPtr)), uintptr(textLen),
		font, uintptr(unsafe.Pointer(&rect)), s.strFormat, brush)
}

func (s *winSplash) drawSpinner(graphics uintptr) {
	cx := float32(winW) / 2
	cy := float32(30+logoSize+8+26+20) + float32(spinnerSize)/2
	r := float32(spinnerSize-3) / 2

	// Wall-clock driven angle: missed/coalesced WM_TIMER ticks don't cause stutter.
	elapsed := time.Since(s.startTime).Seconds()
	angle := float32(math.Mod(elapsed*360.0*1000.0/spinnerPeriodMs, 360.0))

	pGdipDrawArc.Call(graphics, s.trackPen,
		f32bits(cx-r), f32bits(cy-r), f32bits(2*r), f32bits(2*r),
		f32bits(0), f32bits(360))

	pGdipTranslateWorldTransform.Call(graphics, f32bits(cx), f32bits(cy), matrixOrderPrepend)
	pGdipRotateWorldTransform.Call(graphics, f32bits(angle), matrixOrderPrepend)
	pGdipTranslateWorldTransform.Call(graphics, f32bits(-cx), f32bits(-cy), matrixOrderPrepend)

	pGdipDrawArc.Call(graphics, s.arcPen,
		f32bits(cx-r), f32bits(cy-r), f32bits(2*r), f32bits(2*r),
		f32bits(0), f32bits(270))

	pGdipResetWorldTransform.Call(graphics)
}

func (s *winSplash) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Clear running/hwnd last, after shutdownGDIPlus has freed the shared
	// resources, so the next ShowSplash starts a clean run() rather than
	// racing this teardown.
	defer func() {
		s.mu.Lock()
		s.running = false
		s.hwnd = 0
		s.mu.Unlock()
	}()

	s.initGDIPlus()
	defer s.shutdownGDIPlus()

	hInstance, _, _ := pGetModuleHandleW.Call(0)
	className := windows.StringToUTF16Ptr("GoLauncherSplash")

	wndProc := windows.NewCallback(func(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
		switch msg {
		case wmPaint:
			var ps paintStruct
			hdc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
			s.paint(hdc)
			pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
			return 0
		case wmEraseBG:
			return 1
		case wmTimer:
			if wParam == spinnerTimerID {
				pInvalidateRect.Call(hwnd, 0, 0)
			}
			return 0
		case wmDestroy:
			return 0
		}
		ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return ret
	})

	wcx := wndClassExW{
		size:      uint32(unsafe.Sizeof(wndClassExW{})),
		style:     csDropShadow,
		wndProc:   wndProc,
		instance:  windows.Handle(hInstance),
		className: className,
	}
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcx)))

	screenW, _, _ := pGetSystemMetrics.Call(smCxscreen)
	screenH, _, _ := pGetSystemMetrics.Call(smCyscreen)
	x := (screenW - winW) / 2
	y := (screenH - winH) / 2

	hwnd, _, _ := pCreateWindowExW.Call(
		wsExTopmost|wsExToolWin,
		uintptr(unsafe.Pointer(className)), 0,
		wsPopup|wsVisible,
		x, y, winW, winH,
		0, 0, hInstance, 0,
	)
	s.mu.Lock()
	s.hwnd = hwnd
	s.mu.Unlock()

	rgn, _, _ := pCreateRoundRectRgn.Call(0, 0, winW+1, winH+1, cornerRadius*2, cornerRadius*2)
	pSetWindowRgn.Call(hwnd, rgn, 1)

	pSetWindowLongPtrW.Call(hwnd, ^uintptr(3), wndProc) // GWLP_WNDPROC = -4

	pShowWindow.Call(hwnd, swShow)
	pSetForegroundWindow.Call(hwnd)

	s.startTime = time.Now()
	pSetTimer.Call(hwnd, spinnerTimerID, 16, 0)

	go s.dispatchCommands(hwnd)
	s.messageLoop(hwnd)
}

func (s *winSplash) dispatchCommands(hwnd uintptr) {
	for cmd := range s.cmds {
		switch cmd.kind {
		case cmdUpdate:
			s.mu.Lock()
			s.progressPct = cmd.percent
			s.statusText = cmd.text
			s.mu.Unlock()
			pPostMessage.Call(hwnd, wmAppUpdate, 0, 0)
		case cmdHide:
			pPostMessage.Call(hwnd, wmAppHide, 0, 0)
			return
		case cmdError:
			s.mu.Lock()
			s.statusText = cmd.text
			s.mu.Unlock()
			pPostMessage.Call(hwnd, wmAppError, 0, 0)
			return
		}
	}
}

func (s *winSplash) messageLoop(hwnd uintptr) {
	var m winMsg
	for {
		ret, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 || int32(ret) == -1 {
			break
		}
		switch m.message {
		case wmAppUpdate:
			pInvalidateRect.Call(hwnd, 0, 0)
		case wmAppHide:
			pKillTimer.Call(hwnd, spinnerTimerID)
			pShowWindow.Call(hwnd, swHide)
			pDestroyWindow.Call(hwnd)
			return
		case wmAppError:
			pKillTimer.Call(hwnd, spinnerTimerID)
			pShowWindow.Call(hwnd, swHide)
			pDestroyWindow.Call(hwnd)
			s.mu.Lock()
			errText := s.statusText
			s.mu.Unlock()
			errPtr := windows.StringToUTF16Ptr(errText)
			titlePtr := windows.StringToUTF16Ptr(s.cfg.AppName)
			pMessageBoxW.Call(0, uintptr(unsafe.Pointer(errPtr)),
				uintptr(unsafe.Pointer(titlePtr)), mbIconerror)
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}
