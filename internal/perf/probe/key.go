package probe

import (
	"fmt"
	"strconv"
	"strings"
)

// AppleKey is the key for the single Apple Silicon GPU, shared by the macOS
// probes so they merge.
const AppleKey = "apple:0"

// NormalizePCI formats a PCI address as dddd:bb:dd.f in lower case. It
// accepts the 8-digit domain nvidia-smi prints (00000000:01:00.0) and
// addresses without a domain (01:00.0). It returns "" for anything else.
func NormalizePCI(addr string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	parts := strings.Split(addr, ":")
	var domain, bus, devfn string
	switch len(parts) {
	case 2:
		domain, bus, devfn = "0", parts[0], parts[1]
	case 3:
		domain, bus, devfn = parts[0], parts[1], parts[2]
	default:
		return ""
	}
	dev, fn, ok := strings.Cut(devfn, ".")
	if !ok {
		return ""
	}
	d, err1 := strconv.ParseUint(domain, 16, 32)
	b, err2 := strconv.ParseUint(bus, 16, 8)
	v, err3 := strconv.ParseUint(dev, 16, 8)
	f, err4 := strconv.ParseUint(fn, 16, 8)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || d > 0xffff || v > 0x1f || f > 7 {
		return ""
	}
	return FormatPCI(uint32(d), uint32(b), uint32(v), uint32(f))
}

// FormatPCI builds a PCI key from its numeric parts.
func FormatPCI(domain, bus, device, function uint32) string {
	return fmt.Sprintf("%04x:%02x:%02x.%x", domain, bus, device, function)
}

// fallbackKey is used when a probe reports neither a PCI address nor a UUID.
// GPUs with this key cannot be merged across probes.
func fallbackKey(probe string, index int) string {
	return fmt.Sprintf("%s:%d", probe, index)
}

// gpuKey picks the PCI address, then the UUID, then the fallback key.
func gpuKey(pci, uuid, probe string, index int) string {
	if k := NormalizePCI(pci); k != "" {
		return k
	}
	if uuid = strings.TrimSpace(uuid); uuid != "" && !isNA(uuid) {
		return uuid
	}
	return fallbackKey(probe, index)
}

// isNA reports values vendor CLIs print for "not available".
func isNA(v string) bool {
	v = strings.Trim(strings.TrimSpace(v), "[]")
	return v == "" || strings.EqualFold(v, "n/a") || strings.EqualFold(v, "not supported") ||
		strings.EqualFold(v, "unknown")
}

// parseFloat returns nil for empty and not-available values.
func parseFloat(v string) *float64 {
	if isNA(v) {
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return nil
	}
	return &f
}

// parseMB parses a value already in MB.
func parseMB(v string) *int {
	f := parseFloat(v)
	if f == nil || *f < 0 {
		return nil
	}
	return ptr(int(*f))
}

// parseBytesMB parses a byte count and returns it in MB.
func parseBytesMB(v string) *int {
	if isNA(v) {
		return nil
	}
	n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return nil
	}
	return ptr(int(n / bytesPerMB))
}
