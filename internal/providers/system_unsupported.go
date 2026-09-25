//go:build !linux

package providers

import "fmt"

func diskPercent(string) (int, error) {
	return 0, fmt.Errorf("system provider is supported only on Linux")
}
