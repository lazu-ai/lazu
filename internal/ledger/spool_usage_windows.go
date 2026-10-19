//go:build windows

package ledger

import "golang.org/x/sys/windows"

func spoolFreeBytes(path string) (uint64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free uint64
	err = windows.GetDiskFreeSpaceEx(name, &free, nil, nil)
	return free, err
}
