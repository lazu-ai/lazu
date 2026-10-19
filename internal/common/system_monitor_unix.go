//go:build !windows

package common

import "golang.org/x/sys/unix"

// GetDiskSpaceInfo 获取采样路径所在磁盘空间信息（Unix/Linux/macOS）。
func GetDiskSpaceInfo() DiskSpaceInfo {
	path := GetSystemMonitorDiskPath()
	info := DiskSpaceInfo{Path: path}

	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return info
	}

	blockSize := uint64(stat.Bsize)
	info.Total = uint64(stat.Blocks) * blockSize
	info.Free = uint64(stat.Bavail) * blockSize
	info.Used = info.Total - uint64(stat.Bfree)*blockSize
	if info.Total > 0 {
		info.UsedPercent = float64(info.Used) / float64(info.Total) * 100
	}

	return info
}
