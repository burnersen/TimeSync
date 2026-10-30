//go:build windows

package winsys

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// IsElevated reports whether the process runs with administrator rights.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// Domain reports whether the computer is a member of an Active Directory
// domain, and its name.
func Domain() (joined bool, name string) {
	var buf *uint16
	var typ uint32
	if err := windows.NetGetJoinInformation(nil, &buf, &typ); err != nil {
		return false, ""
	}
	defer windows.NetApiBufferFree((*byte)(unsafe.Pointer(buf)))
	if typ == windows.NetSetupDomainName {
		return true, windows.UTF16PtrToString(buf)
	}
	return false, ""
}

// TimeZone describes the configured time zone.
type TimeZone struct {
	KeyName      string `json:"keyName"`
	StandardName string `json:"standardName"`
	DaylightName string `json:"daylightName"`
	// UTCOffsetMinutes is the current offset including daylight saving time.
	UTCOffsetMinutes int  `json:"utcOffsetMinutes"`
	HasDST           bool `json:"hasDst"`
	InDST            bool `json:"inDst"`
	// AutoDSTDisabled: "Adjust for daylight saving time automatically" is
	// switched off although the zone has daylight saving time.
	AutoDSTDisabled bool `json:"autoDstDisabled"`
	// AutoTimeZone: "Set time zone automatically" is on.
	AutoTimeZone bool `json:"autoTimeZone"`
}

type dynamicTimeZoneInformation struct {
	Bias                        int32
	StandardName                [32]uint16
	StandardDate                windows.Systemtime
	StandardBias                int32
	DaylightName                [32]uint16
	DaylightDate                windows.Systemtime
	DaylightBias                int32
	TimeZoneKeyName             [128]uint16
	DynamicDaylightTimeDisabled uint8
}

// CurrentTimeZone reads the time zone settings.
func CurrentTimeZone() TimeZone {
	var tzi dynamicTimeZoneInformation
	id, _, _ := procGetDynamicTimeZoneInformation.Call(uintptr(unsafe.Pointer(&tzi)))
	_, off := time.Now().Zone()
	tz := TimeZone{
		KeyName:          windows.UTF16ToString(tzi.TimeZoneKeyName[:]),
		StandardName:     windows.UTF16ToString(tzi.StandardName[:]),
		DaylightName:     windows.UTF16ToString(tzi.DaylightName[:]),
		UTCOffsetMinutes: off / 60,
		HasDST:           tzi.DaylightDate.Month != 0,
		InDST:            id == 2,
		AutoDSTDisabled:  tzi.DynamicDaylightTimeDisabled != 0,
	}
	if tz.AutoDSTDisabled {
		tz.HasDST = true // the zone has DST, it is only switched off
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\tzautoupdate`, registry.QUERY_VALUE); err == nil {
		start, _, _ := k.GetIntegerValue("Start")
		tz.AutoTimeZone = start == 3
		k.Close()
	}
	return tz
}

// OSVersion returns e.g. "Windows 10.0.26100 (x64)".
func OSVersion() string {
	v := windows.RtlGetVersion()
	arch := "x64"
	var machine, native uint16
	if windows.IsWow64Process2(windows.CurrentProcess(), &machine, &native) == nil && native == 0xAA64 {
		arch = "ARM64"
	}
	return fmt.Sprintf("Windows %d.%d.%d (%s)", v.MajorVersion, v.MinorVersion, v.BuildNumber, arch)
}

// UILanguage returns the user's display language, e.g. "de-DE".
func UILanguage() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return "en-US"
	}
	return langs[0]
}

// LaunchedFromExplorer reports whether this console program got a console
// window of its own (double-click in Explorer) rather than running inside
// an existing command prompt. Such a window closes as soon as the program
// ends, so the program should wait for a key press first.
func LaunchedFromExplorer() bool {
	var pids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), 4)
	return n == 1
}

// ErrCancelled is returned when the user declines the UAC prompt.
var ErrCancelled = errors.New("the administrator prompt was cancelled")

type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           windows.Handle
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       windows.Handle
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      windows.Handle
	dwHotKey       uint32
	hIconOrMonitor windows.Handle
	hProcess       windows.Handle
}

var (
	shell32             = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = shell32.NewProc("ShellExecuteExW")
)

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	swHide                = 0
)

// RunElevated starts exe with administrator rights (the user sees the UAC
// prompt), waits for it and returns its exit code. The window stays hidden.
func RunElevated(exe string, args []string) (uint32, error) {
	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync,
		lpVerb:       windows.StringToUTF16Ptr("runas"),
		lpFile:       windows.StringToUTF16Ptr(exe),
		lpParameters: windows.StringToUTF16Ptr(windows.ComposeCommandLine(args)),
		nShow:        swHide,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if ret, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); ret == 0 {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return 0, ErrCancelled
		}
		return 0, err
	}
	if info.hProcess == 0 {
		return 0, errors.New("the elevated process could not be tracked")
	}
	defer windows.CloseHandle(info.hProcess)
	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return 0, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 0, err
	}
	return code, nil
}

// OpenURL opens a web page or a settings page such as "ms-settings:dateandtime".
func OpenURL(url string) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "ms-settings:") {
		return fmt.Errorf("refusing to open %q", url)
	}
	return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(url), nil, nil, windows.SW_SHOWNORMAL)
}

// RedactUser replaces the user profile path in s, so diagnostics can be
// shared without revealing the user name.
func RedactUser(s string) string {
	if p := os.Getenv("USERPROFILE"); p != "" {
		s = strings.ReplaceAll(s, p, "%USERPROFILE%")
	}
	if u := os.Getenv("USERNAME"); len(u) > 2 {
		s = strings.ReplaceAll(s, u, "<user>")
	}
	return s
}
