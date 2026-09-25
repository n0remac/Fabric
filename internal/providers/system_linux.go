//go:build linux

package providers

import (
	"fmt"
	"math"

	"golang.org/x/sys/unix"
)

func diskPercent(path string) (int, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	if stat.Blocks == 0 || stat.Bavail > stat.Blocks {
		return 0, fmt.Errorf("invalid filesystem counters")
	}
	used := stat.Blocks - stat.Bavail
	return clampPercent(int(math.Round(float64(used) * 100 / float64(stat.Blocks)))), nil
}
