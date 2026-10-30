//go:build windows

package winsys

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// SDDL building blocks. Rights: FA = full, 0x1200a9 = read & execute,
// 0x1301bf = modify. OICI = applies to files and subfolders.
const (
	sddlSystemFull = "(A;OICI;FA;;;SY)"
	sddlAdminsFull = "(A;OICI;FA;;;BA)"
	sddlUsersRead  = "(A;OICI;0x1200a9;;;AU)"
	sddlLocalSvcRd = "(A;OICI;0x1200a9;;;LS)"
)

// DataDirSDDL protects C:\ProgramData\TimeSync: only administrators may
// change the settings, everyone may read them.
const DataDirSDDL = "D:P" + sddlSystemFull + sddlAdminsFull + sddlLocalSvcRd + sddlUsersRead

// StateDirSDDL additionally lets the service (by its own service SID) write
// its log and history.
func StateDirSDDL(serviceSID string) string {
	return DataDirSDDL + "(A;OICI;0x1301bf;;;" + serviceSID + ")"
}

// SecureDir creates dir if needed and replaces its permissions, without
// inheriting anything from the parent folder.
func SecureDir(dir, sddl string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("security descriptor: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// ServiceSID returns the SID Windows assigns to a service ("NT SERVICE\name").
func ServiceSID(name string) (string, error) {
	sid, _, _, err := windows.LookupSID("", `NT SERVICE\`+name)
	if err != nil {
		return "", err
	}
	return sid.String(), nil
}
