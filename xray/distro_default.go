//go:build !darwin

// Default distro for non-Apple platforms (Android, Linux, Windows).
//
// Android, Linux, Windows не имеют 50MB NE-style hard cap, поэтому используем
// полный upstream distro/all (включая gVisor proxy/tun + proxy/wireguard).
//
// См. xray/distro_apple.go для trimmed Apple distro и обоснования (jetsam).
package xray

import (
	_ "github.com/xtls/xray-core/main/distro/all"
)
