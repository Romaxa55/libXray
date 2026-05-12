//go:build darwin

// Apple-platforms (iOS, iPad, macOS, maccatalyst) distro for libXray.
//
// Цель: maximum coverage минус единственный реальный memory hog (gVisor).
//
// Background:
// Performance-engineer bin-analysis 2026-05-12 показал что Go linker уже
// dead-strip'ает unused code (size бинаря одинаковый что с 17 imports,
// что с 47). НО есть исключение: proxy/tun (gVisor) аллоцирует ~28MB BSS
// (zero-init) при загрузке xcframework — это **всегда** в RSS, даже если
// proxy/tun не зарегистрирован в xray-конфиге.
//
// iOS NetworkExtension hard cap 50MB RSS (jetsam kill). С gVisor =
// ~44MB active → 6MB headroom (опасно). Без gVisor = ~16-18MB active →
// 30+MB headroom (комфорт).
//
// На Apple используем tun2socks (Tun2SocksKit с hev-socks5-tunnel) вместо
// xray native TUN — это де-факто стандарт у всех iOS xray-клиентов
// (Hiddify Mango, v2RayTun, Streisand). Native TUN в xray-core по словам
// RPRX (maintainer) — "planned 2029".
//
// What's included (everything from distro/all minus the gVisor module + CLI):
//
//   App services:
//     dispatcher, proxyman/in+out (mandatory)
//     dns           (config has dns.servers list)
//     log           (config has loglevel:"debug")
//     observatory   (burstObservatory + leastPing strategy)
//     policy        (we use "policy" block in config)
//     router        (balancers + rules)
//     stats         (REQUIRED to parse policy block even with statsXxx=false)
//
//   Inbound proxies (local listeners for tun2socks / SOCKS clients):
//     socks         main-socks (10800) + directSocks (1087)
//     http          http-proxy (10900)
//     dokodemo      api stats (53675)
//
//   Outbound proxies (server protocols + fallback resilience):
//     vless         78% of servers in data_v5.dat
//     trojan        11%
//     shadowsocks   8%
//     shadowsocks_2022 (rare but present)
//     vmess         2.5%
//     wireguard     fallback if VLESS gets banned
//     freedom       fragment outbound (anti-DPI) + direct
//     blackhole     block routing rule
//     dns           DNS interception outbound
//
//   Transports (everything except niche stuff):
//     tcp, udp, tls, websocket   (baseline)
//     reality                    (modern masking)
//     grpc, httpupgrade, splithttp  (some servers use these)
//     kcp                         (mKCP, legacy)
//
//   Transport headers:
//     http (camouflage), noop (default)
//
// What's EXCLUDED and WHY:
//   proxy/tun           ← gVisor +28MB BSS, killer for iOS NE
//   proxy/loopback      ← we are client, not relay
//   proxy/vless/inbound ← we are client (xray as server-mode disabled)
//   proxy/vmess/inbound ← same
//   app/commander, app/log/command, app/proxyman/command, app/stats/command,
//   app/observatory/command   ← gRPC runtime control API (no need in mobile)
//   app/metrics, app/reverse, app/dns/fakedns ← not used
//   main/toml, main/yaml ← we only use JSON
//   main/confloader/external ← we don't load config from URL
//   main/commands/all ← CLI commands
//
// IMPORTANT: If you add a new server protocol to backend/DB, add the matching
// proxy/* import here AND rebuild xcframework:
//
//   python3 libXray/build/main.py apple gomobile
//
package xray

import (
	// Mandatory core registrations — core.New() panics without these.
	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"

	// App services.
	_ "github.com/xtls/xray-core/app/dns"
	_ "github.com/xtls/xray-core/app/log"
	_ "github.com/xtls/xray-core/app/observatory"
	_ "github.com/xtls/xray-core/app/policy"
	_ "github.com/xtls/xray-core/app/router"
	_ "github.com/xtls/xray-core/app/stats"

	// Fix dependency cycle (core import in internet package).
	_ "github.com/xtls/xray-core/transport/internet/tagged/taggedimpl"

	// Inbound proxies (local listeners).
	_ "github.com/xtls/xray-core/proxy/dokodemo"
	_ "github.com/xtls/xray-core/proxy/http"
	_ "github.com/xtls/xray-core/proxy/socks"

	// Outbound proxies — all server protocols from data_v5.dat + resilience.
	_ "github.com/xtls/xray-core/proxy/blackhole"
	_ "github.com/xtls/xray-core/proxy/dns"
	_ "github.com/xtls/xray-core/proxy/freedom"
	_ "github.com/xtls/xray-core/proxy/shadowsocks"
	_ "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	_ "github.com/xtls/xray-core/proxy/trojan"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	_ "github.com/xtls/xray-core/proxy/vmess/outbound"
	_ "github.com/xtls/xray-core/proxy/wireguard"

	// Transports.
	_ "github.com/xtls/xray-core/transport/internet/grpc"
	_ "github.com/xtls/xray-core/transport/internet/httpupgrade"
	_ "github.com/xtls/xray-core/transport/internet/kcp"
	_ "github.com/xtls/xray-core/transport/internet/reality"
	_ "github.com/xtls/xray-core/transport/internet/splithttp"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
	_ "github.com/xtls/xray-core/transport/internet/udp"
	_ "github.com/xtls/xray-core/transport/internet/websocket"

	// Transport headers.
	_ "github.com/xtls/xray-core/transport/internet/headers/http"
	_ "github.com/xtls/xray-core/transport/internet/headers/noop"

	// JSON config loader (our only format).
	_ "github.com/xtls/xray-core/main/json"
)
