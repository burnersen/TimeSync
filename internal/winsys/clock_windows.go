//go:build windows

// Package winsys wraps the Windows functions TimeSync needs. Everything
// here is a thin layer over documented Win32 calls; the decisions are made
// elsewhere (package engine) where they can be tested on any system.
package winsys

import (
	"errors"
	"fmt"
	"math"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/burnersen/TimeSync/internal/engine"
	"github.com/burnersen/TimeSync/internal/syncer"
)

var (
	kernel32                           = windows.NewLazySystemDLL("kernel32.dll")
	procSetSystemTime                  = kernel32.NewProc("SetSystemTime")
	procGetSystemTimeAdjustmentPrecise = kernel32.NewProc("GetSystemTimeAdjustmentPrecise")
	procSetSystemTimeAdjustmentPrecise = kernel32.NewProc("SetSystemTimeAdjustmentPrecise")
	procGetTickCount64                 = kernel32.NewProc("GetTickCount64")
	procGetConsoleProcessList          = kernel32.NewProc("GetConsoleProcessList")
	procGetDynamicTimeZoneInformation  = kernel32.NewProc("GetDynamicTimeZoneInformation")

	advapi32                  = windows.NewLazySystemDLL("advapi32.dll")
	procAdjustTokenPrivileges = advapi32.NewProc("AdjustTokenPrivileges")
)

// Clock implements syncer.Clock on the real system clock.
type Clock struct{}

// PreciseNow reads the system clock with sub-millisecond resolution.
func PreciseNow() time.Time {
	var ft windows.Filetime
	windows.GetSystemTimePreciseAsFileTime(&ft)
	return time.Unix(0, ft.Nanoseconds())
}

// Step sets the clock forward or back by offset.
func (Clock) Step(offset time.Duration) error {
	if err := EnablePrivilege("SeSystemtimePrivilege"); err != nil {
		return err
	}
	return setSystemTime(PreciseNow().Add(offset))
}

func setSystemTime(t time.Time) error {
	utc := t.UTC()
	st := windows.Systemtime{
		Year:         uint16(utc.Year()),
		Month:        uint16(utc.Month()),
		DayOfWeek:    uint16(utc.Weekday()),
		Day:          uint16(utc.Day()),
		Hour:         uint16(utc.Hour()),
		Minute:       uint16(utc.Minute()),
		Second:       uint16(utc.Second()),
		Milliseconds: uint16(utc.Nanosecond() / 1e6),
	}
	if ret, _, err := procSetSystemTime.Call(uintptr(unsafe.Pointer(&st))); ret == 0 {
		return fmt.Errorf("SetSystemTime: %w", err)
	}
	return nil
}

// Slew makes the clock run slightly faster or slower. The change stays in
// effect until StopSlew is called; the service does that when the planned
// duration is over, before every new measurement, and when it stops.
func (Clock) Slew(plan engine.SlewPlan) error {
	if procGetSystemTimeAdjustmentPrecise.Find() != nil || procSetSystemTimeAdjustmentPrecise.Find() != nil {
		return syncer.ErrSlewUnsupported
	}
	if err := EnablePrivilege("SeSystemtimePrivilege"); err != nil {
		return err
	}
	var adj, inc uint64
	var disabled int32
	ret, _, err := procGetSystemTimeAdjustmentPrecise.Call(
		uintptr(unsafe.Pointer(&adj)), uintptr(unsafe.Pointer(&inc)), uintptr(unsafe.Pointer(&disabled)))
	if ret == 0 || inc == 0 {
		return fmt.Errorf("GetSystemTimeAdjustmentPrecise: %w", err)
	}
	// The clock advances by adj per tick of length inc. Equal values mean
	// normal speed; a larger adj makes it run fast.
	newAdj := uint64(math.Round(float64(inc) * (1 + plan.RatePPM*1e-6)))
	if ret, _, err := procSetSystemTimeAdjustmentPrecise.Call(uintptr(newAdj), 0); ret == 0 {
		return fmt.Errorf("SetSystemTimeAdjustmentPrecise: %w", err)
	}
	return nil
}

// StopSlew hands the clock speed back to Windows.
func StopSlew() error {
	if procSetSystemTimeAdjustmentPrecise.Find() != nil {
		return nil
	}
	if err := EnablePrivilege("SeSystemtimePrivilege"); err != nil {
		return err
	}
	if ret, _, err := procSetSystemTimeAdjustmentPrecise.Call(0, 1); ret == 0 {
		return fmt.Errorf("SetSystemTimeAdjustmentPrecise: %w", err)
	}
	return nil
}

// EnablePrivilege switches on a privilege the process token holds but has
// disabled. It fails if the token does not hold it at all (no admin rights).
func EnablePrivilege(name string) error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &luid); err != nil {
		return err
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	// Called directly instead of through windows.AdjustTokenPrivileges: a
	// privilege the token does not hold is reported as success with
	// ERROR_NOT_ALL_ASSIGNED as the last error, and only the raw call hands
	// that value back reliably.
	ret, _, callErr := procAdjustTokenPrivileges.Call(uintptr(token), 0, uintptr(unsafe.Pointer(&tp)), 0, 0, 0)
	if ret == 0 {
		return callErr
	}
	if errors.Is(callErr, windows.ERROR_NOT_ALL_ASSIGNED) {
		return ErrNoPrivilege
	}
	return nil
}

// ErrNoPrivilege means the process may not change the system time.
var ErrNoPrivilege = errors.New("changing the system time requires administrator rights")

// Uptime is the time since Windows started (sleep included).
func Uptime() time.Duration {
	ms, _, _ := procGetTickCount64.Call()
	return time.Duration(ms) * time.Millisecond
}

// BootTime is the moment Windows started, rounded to the second so that
// repeated calls agree.
func BootTime() time.Time {
	return time.Now().Add(-Uptime()).Truncate(time.Second)
}
