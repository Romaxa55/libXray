package xray

import (
	"os"
	"runtime/debug"
	"strconv"

	"github.com/xtls/libxray/memory"
	"github.com/xtls/xray-core/common/cmdarg"
	"github.com/xtls/xray-core/common/platform"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
	"github.com/xtls/xray-core/main/commands/base"

	// Платформо-зависимая регистрация протоколов:
	//   - Apple (iOS/macOS/maccatalyst): distro_apple.go — без gVisor (proxy/tun)
	//     → экономит ~28MB RSS, критично для iOS NE jetsam 50MB cap
	//   - Прочее (Android/Linux/Win): distro_default.go — полный distro/all
	// См. detailed analysis в комментариях этих файлов.
)

var (
	coreServer *core.Instance
)

func StartXray(configPath string) (*core.Instance, error) {
	file := cmdarg.Arg{configPath}
	config, err := core.LoadConfig("json", file)
	if err != nil {
		return nil, err
	}

	server, err := core.New(config)
	if err != nil {
		return nil, err
	}

	return server, nil
}

func StartXrayFromJSON(configJSON string) (*core.Instance, error) {
	// Convert JSON string to bytes
	configBytes := []byte(configJSON)

	// Use core.StartInstance which can load configuration directly from bytes
	server, err := core.StartInstance("json", configBytes)
	if err != nil {
		return nil, err
	}

	return server, nil
}

// SetTunFd sets the TUN file descriptor.
// Call this BEFORE RunXray/RunXrayFromJSON.
func SetTunFd(fd int32) {
	os.Setenv(platform.TunFdKey, strconv.Itoa(int(fd)))
}

func InitEnv(datDir string, mphCachePath string) {
	os.Setenv(platform.AssetLocation, datDir)
	os.Setenv(platform.CertLocation, datDir)

	// v26.5.9 убрал MPH-cache из xray-core (см. release notes 2026-05).
	// mphCachePath аргумент оставлен для backward compat caller-API (мобила/backend),
	// но больше не используется.
	_ = mphCachePath
}

// Run Xray instance.
// datDir means the dir which geosite.dat and geoip.dat are in.
// mphCachePath means the path of mph cache file. leave it empty if you don't use mph cache.
// configPath means the config.json file path.
func RunXray(datDir string, mphCachePath string, configPath string) (err error) {
	InitEnv(datDir, mphCachePath)
	memory.InitForceFree()
	coreServer, err = StartXray(configPath)
	if err != nil {
		return
	}

	if err = coreServer.Start(); err != nil {
		return
	}

	syncCoreServerPtr() // 2026-05-18: thread-safe snapshot для ProbeOutbound
	debug.FreeOSMemory()
	return nil
}

// Run Xray instance with JSON configuration string.
// datDir means the dir which geosite.dat and geoip.dat are in.
// mphCachePath means the path of mph cache file. leave it empty if you don't use mph cache.
// configJSON means the JSON configuration string.
func RunXrayFromJSON(datDir string, mphCachePath string, configJSON string) (err error) {
	InitEnv(datDir, mphCachePath)
	memory.InitForceFree()
	coreServer, err = StartXrayFromJSON(configJSON)
	if err != nil {
		return
	}

	syncCoreServerPtr() // 2026-05-18: thread-safe snapshot для ProbeOutbound
	debug.FreeOSMemory()
	return nil
}

// Get Xray State
func GetXrayState() bool {
	return coreServer != nil && coreServer.IsRunning()
}

// Stop Xray instance.
func StopXray() error {
	if coreServer != nil {
		err := coreServer.Close()
		coreServer = nil
		syncCoreServerPtr() // 2026-05-18: thread-safe snapshot для ProbeOutbound
		if err != nil {
			return err
		}
	}
	return nil
}

// Xray's version
func XrayVersion() string {
	return core.Version()
}

// BuildMphCache — deprecated в xray-core v26.5.9 (MPH-cache убран как
// механизм оптимизации). Функция оставлена для backward compat caller-API,
// но больше не делает ничего полезного: парсит config чтобы не сломать
// existing scripts которые проверяют валидность конфига до RunXray.
func BuildMphCache(datDir string, mphCachePath string, configPath string) error {
	_ = mphCachePath // unused after v26.5.9
	InitEnv(datDir, "")
	cf, err := os.Open(configPath)
	if err != nil {
		base.Fatalf("failed to open config file: %v", err)
		return err
	}
	defer cf.Close()

	if _, err := serial.DecodeJSONConfig(cf); err != nil {
		base.Fatalf("failed to decode config file: %v", err)
		return err
	}
	return nil
}
