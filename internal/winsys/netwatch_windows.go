//go:build windows

package winsys

import (
	"golang.org/x/sys/windows"
)

var (
	iphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procNotifyAddrChange = iphlpapi.NewProc("NotifyAddrChange")
)

// WatchNetwork sends a signal on ch whenever an IP address of this computer
// changes: a cable plugged in, a new WLAN, a VPN connected. Signals are
// dropped while the receiver is busy; the caller debounces anyway.
func WatchNetwork(ch chan<- struct{}) {
	if procNotifyAddrChange.Find() != nil {
		return
	}
	go func() {
		for {
			// With both arguments NULL the call blocks until a change.
			if ret, _, _ := procNotifyAddrChange.Call(0, 0); ret != 0 {
				return
			}
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}()
}
