package utils

import (
	"runtime"
	"unsafe"
	"github.com/rodrigocfd/windigo/win"
    "github.com/rodrigocfd/windigo/win/co"
)

func TakeScreenShot() {
	runtime.LockOSThread()

	cxScreen := win.GetSystemMetrics(co.SM_CXSCREEN)
	cyScreen := win.GetSystemMetrics(co.SM_CYSCREEN)

	hdcScreen := win.HWND(0).GetDC()
	defer win.HWND(0).ReleaseDC(hdcScreen)

	hBmp := hdcScreen.CreateCompatibleBitmap(cxScreen, cyScreen)
	defer hBmp.DeleteObject()

	hdcMem := hdcScreen.CreateCompatibleDC()
	defer hdcMem.DeleteDC()

	hBmpOld := hdcMem.SelectObjectBitmap(hBmp)
	defer hdcMem.SelectObjectBitmap(hBmpOld)

	hdcMem.BitBlt(
		win.POINT{X: 0, Y: 0},
		win.SIZE{Cx: cxScreen, Cy: cyScreen},
		hdcScreen,
		win.POINT{X: 0, Y: 0},
		co.ROP_SRCCOPY,
	)

	bi := win.BITMAPINFO{
		BmiHeader: win.BITMAPINFOHEADER{
			BiWidth:       cxScreen,
			BiHeight:      cyScreen,
			BiPlanes:      1,
			BiBitCount:    32,
			BiCompression: co.BI_RGB,
		},
	}
	bi.BmiHeader.SetBiSize()

	bmpObj := win.BITMAP{}
	hBmp.GetObject(&bmpObj)
	bmpSize := bmpObj.CalcBitmapSize(bi.BmiHeader.BiBitCount)

	rawMem := win.GlobalAlloc(co.GMEM_FIXED|co.GMEM_ZEROINIT, bmpSize)
	defer rawMem.GlobalFree()

	bmpSlice := rawMem.GlobalLock(int(bmpSize))
	defer rawMem.GlobalUnlock()

	hdcScreen.GetDIBits(hBmp, 0, int(cyScreen), bmpSlice, &bi, co.DIB_RGB_COLORS)

	bfh := win.BITMAPFILEHEADER{}
	bfh.SetBfType()
	bfh.SetBfOffBits(uint32(unsafe.Sizeof(bfh) + unsafe.Sizeof(bi.BmiHeader)))
	bfh.SetBfSize(bfh.BfOffBits() + uint32(bmpSize))

	fo, _ := win.FileOpen("C:\\users\\rodrigo\\desktop\\a.bmp", co.FILE_OPEN_RW_OPEN_OR_CREATE)
	defer fo.Close()

	fo.Write(bfh.Serialize())
	fo.Write(bi.BmiHeader.Serialize())
	fo.Write(bmpSlice)

	println("Done")
}
