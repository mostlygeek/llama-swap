//go:build windows && !amd64

package hw

import "fmt"

func detectDXGI() ([]detectedAccelerator, error) {
	return nil, fmt.Errorf("DXGI hardware detection is not implemented on this architecture")
}
