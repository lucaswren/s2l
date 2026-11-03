//go:build linux

package debuglog

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func platformResources(r *Resources, dataDir string) {
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "VmRSS:" {
				n, _ := strconv.ParseUint(fields[1], 10, 64)
				r.RSSBytes = n * 1024
			}
		}
	}
	if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		r.OpenFDs = len(entries)
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dataDir, &stat); err == nil {
		r.DiskFreeBytes = stat.Bavail * uint64(stat.Bsize)
		r.DiskSampled = true
	} else {
		r.Error = "data directory disk statistics unavailable"
	}
}
