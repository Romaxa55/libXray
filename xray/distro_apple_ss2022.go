//go:build darwin && !zeus_nogpl

// Shadowsocks 2022 is built on github.com/sagernet/sing-shadowsocks and github.com/sagernet/sing (GPL-3.0-or-later).
// ZEUS builds its core with the zeus_nogpl tag and goes without it (Xray-core's infra/conf then refuses a 2022 config
// with a plain error); every other Apple build — MegaV's — keeps it.

package xray

import (
	_ "github.com/xtls/xray-core/proxy/shadowsocks_2022"
)
