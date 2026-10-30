package applist

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"syscall"
	"unsafe"
)

var (
	modShell32 = syscall.NewLazyDLL("shell32.dll")
	modGdi32   = syscall.NewLazyDLL("gdi32.dll")

	procSHGetFileInfoW     = modShell32.NewProc("SHGetFileInfoW")
	procGetIconInfo        = modUser32.NewProc("GetIconInfo")
	procDestroyIcon        = modUser32.NewProc("DestroyIcon")
	procGetObjectW         = modGdi32.NewProc("GetObjectW")
	procCreateCompatibleDC = modGdi32.NewProc("CreateCompatibleDC")
	procGetDIBits          = modGdi32.NewProc("GetDIBits")
	procDeleteDC           = modGdi32.NewProc("DeleteDC")
	procDeleteObject       = modGdi32.NewProc("DeleteObject")
)

const (
	shgfiIcon      = 0x100
	shgfiLargeIcon = 0x0
	dibRGBColors   = 0
)

type shFileInfo struct {
	HIcon       uintptr
	IIcon       int32
	Attributes  uint32
	DisplayName [260]uint16
	TypeName    [80]uint16
}

type iconInfoW struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

type bitmapObj struct {
	Type       int32
	Width      int32
	Height     int32
	WidthBytes int32
	Planes     uint16
	BitsPixel  uint16
	Bits       uintptr
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func extractIconBase64(exePath string) string {
	if exePath == "" {
		return ""
	}
	pathPtr, _ := syscall.UTF16PtrFromString(exePath)

	var sfi shFileInfo
	ret, _, _ := procSHGetFileInfoW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		uintptr(unsafe.Pointer(&sfi)),
		unsafe.Sizeof(sfi),
		shgfiIcon|shgfiLargeIcon,
	)
	if ret == 0 || sfi.HIcon == 0 {
		return ""
	}
	defer procDestroyIcon.Call(sfi.HIcon)

	var ii iconInfoW
	ret2, _, _ := procGetIconInfo.Call(sfi.HIcon, uintptr(unsafe.Pointer(&ii)))
	if ret2 == 0 {
		return ""
	}
	if ii.HbmMask != 0 {
		defer procDeleteObject.Call(ii.HbmMask)
	}
	if ii.HbmColor == 0 {
		return ""
	}
	defer procDeleteObject.Call(ii.HbmColor)

	var bm bitmapObj
	procGetObjectW.Call(ii.HbmColor, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm)))
	if bm.Width == 0 || bm.Height == 0 {
		return ""
	}

	w, h := int(bm.Width), int(bm.Height)

	bih := bitmapInfoHeader{
		Size:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:    int32(w),
		Height:   -int32(h), // top-down
		Planes:   1,
		BitCount: 32,
	}

	pixels := make([]byte, w*h*4)

	hdc, _, _ := procCreateCompatibleDC.Call(0)
	if hdc == 0 {
		return ""
	}
	defer procDeleteDC.Call(hdc)

	procGetDIBits.Call(
		hdc,
		ii.HbmColor,
		0,
		uintptr(h),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bih)),
		dibRGBColors,
	)

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	hasAlpha := false
	for i := 3; i < len(pixels); i += 4 {
		if pixels[i] != 0 {
			hasAlpha = true
			break
		}
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := (y*w + x) * 4
			b, g, r, a := pixels[off], pixels[off+1], pixels[off+2], pixels[off+3]
			if !hasAlpha {
				a = 255
			}
			img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: a})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}
