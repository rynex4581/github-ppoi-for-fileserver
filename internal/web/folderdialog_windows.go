//go:build windows

package web

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// Windows 標準の「フォルダーの選択」ダイアログ（IFileOpenDialog の FOS_PICKFOLDERS）を開く。
// ブラウザからはフォルダのフルパスを取れないため、同じ端末で動いている本体側で開き、
// 選ばれたパスを画面に返す（設定画面のデータルート登録で使う）。
// COM は cgo 無しで vtable を直接呼ぶ。呼び出しは 1 つの OS スレッドに固定する。

var (
	ole32  = syscall.NewLazyDLL("ole32.dll")
	user32 = syscall.NewLazyDLL("user32.dll")

	procCoInitializeEx      = ole32.NewProc("CoInitializeEx")
	procCoUninitialize      = ole32.NewProc("CoUninitialize")
	procCoCreateInstance    = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
)

// errFolderDialogUnsupported は Windows では返さないが、呼ぶ側の比較のために置く。
var errFolderDialogUnsupported = errors.New("folder dialog is only available on Windows")

type comGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	// CLSID_FileOpenDialog {DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7}
	clsidFileOpenDialog = comGUID{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	// IID_IFileOpenDialog {d57c7288-d4ad-4768-be02-9d969532d960}
	iidIFileOpenDialog = comGUID{0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
)

const (
	coinitApartmentThreaded = 0x2
	clsctxInprocServer      = 0x1
	fosPickFolders          = 0x20
	fosForceFileSystem      = 0x40
	sigdnFileSysPath        = 0x80058000
	hrCancelled             = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)
	rpcEChangedMode         = 0x80010106 // 既に別のモードで CoInitialize 済み

	// vtable の位置。IUnknown（0〜2）、IModalWindow::Show（3）、IFileDialog の各メソッド（4〜）
	vtRelease     = 2
	vtShow        = 3
	vtSetOptions  = 9
	vtGetOptions  = 10
	vtSetTitle    = 17
	vtGetResult   = 20
	vtDisplayName = 5 // IShellItem::GetDisplayName
)

// comObj は COM オブジェクト。先頭が vtable へのポインタ。
type comObj struct {
	vtbl *[32]uintptr
}

// comCall は COM オブジェクトの vtable の idx 番目を呼ぶ。
func comCall(obj *comObj, idx int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(obj.vtbl[idx], append([]uintptr{uintptr(unsafe.Pointer(obj))}, args...)...)
	return r
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*uint16)(unsafe.Add(unsafe.Pointer(p), n*2)) != 0 {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n))
}

// pickFolder はフォルダ選択のダイアログを開き、選ばれたパスを返す。取り消しなら cancelled が真。
// 直前までブラウザを操作しているはずなので、前面のウィンドウを親にして手前に出す。
func pickFolder(title string) (path string, cancelled bool, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if failed(hr) && uint32(hr) != rpcEChangedMode {
		return "", false, fmt.Errorf("CoInitializeEx: 0x%08X", uint32(hr))
	}
	if !failed(hr) {
		defer procCoUninitialize.Call()
	}

	var dlg *comObj
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)), uintptr(unsafe.Pointer(&dlg)))
	if failed(hr) || dlg == nil {
		return "", false, fmt.Errorf("CoCreateInstance(FileOpenDialog): 0x%08X", uint32(hr))
	}
	defer comCall(dlg, vtRelease)

	var opts uint32
	comCall(dlg, vtGetOptions, uintptr(unsafe.Pointer(&opts)))
	comCall(dlg, vtSetOptions, uintptr(opts|fosPickFolders|fosForceFileSystem))
	if title != "" {
		if t, err := syscall.UTF16PtrFromString(title); err == nil {
			comCall(dlg, vtSetTitle, uintptr(unsafe.Pointer(t)))
		}
	}

	owner, _, _ := procGetForegroundWindow.Call()
	hr = comCall(dlg, vtShow, owner)
	if uint32(hr) == hrCancelled {
		return "", true, nil
	}
	if failed(hr) {
		return "", false, fmt.Errorf("IFileDialog::Show: 0x%08X", uint32(hr))
	}

	var item *comObj
	hr = comCall(dlg, vtGetResult, uintptr(unsafe.Pointer(&item)))
	if failed(hr) || item == nil {
		return "", false, fmt.Errorf("IFileDialog::GetResult: 0x%08X", uint32(hr))
	}
	defer comCall(item, vtRelease)

	var p *uint16
	hr = comCall(item, vtDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&p)))
	if failed(hr) || p == nil {
		return "", false, errors.New("選んだ場所はファイルシステム上のフォルダではない")
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return utf16PtrToString(p), false, nil
}
