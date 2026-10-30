//go:build windows

package winsys

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
)

func isAlreadyExists(err error) bool {
	return errors.Is(err, windows.ERROR_ALREADY_EXISTS) || strings.Contains(err.Error(), "registry key already exists")
}
