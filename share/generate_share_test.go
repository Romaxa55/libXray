package share

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xtls/xray-core/infra/conf"
)

func buildHy2Outbound(t *testing.T, auth string, host string, port uint16, streamSetting *conf.StreamConfig) conf.OutboundDetourConfig {
	t.Helper()
	settings := &conf.HysteriaClientConfig{
		Address: parseAddress(host),
		Port:    port,
	}
	settingsJSON, err := json.Marshal(settings)
	require.NoError(t, err)
	return conf.OutboundDetourConfig{
		Protocol:      "hysteria",
		Settings:      new(json.RawMessage(settingsJSON)),
		StreamSetting: streamSetting,
	}
}

func buildHy2StreamSettings(auth string, tls *conf.TLSConfig, finalMask *conf.FinalMask) *conf.StreamConfig {
	ss := &conf.StreamConfig{
		Network:  new(conf.TransportProtocol("hysteria")),
		Security: "tls",
	}
	ss.TLSSettings = tls
	ss.HysteriaSettings = &conf.HysteriaConfig{
		Version: 2,
		Auth:    auth,
	}
	ss.FinalMask = finalMask
	return ss
}

func TestGenerate_Hy2_WithBandwidth(t *testing.T) {
	tls := &conf.TLSConfig{ServerName: "example.com"}
	fm := &conf.FinalMask{
		QuicParams: &conf.QuicParamsConfig{
			Congestion: "brutal",
			BrutalUp:   conf.Bandwidth("100 mbps"),
			BrutalDown: conf.Bandwidth("200 mbps"),
		},
	}
	ss := buildHy2StreamSettings("auth", tls, fm)
	outbound := buildHy2Outbound(t, "auth", "host", 443, ss)

	link, err := shareLink(outbound)
	require.NoError(t, err)

	assert.Equal(t, "hysteria2", link.Scheme)
	q := link.Query()
	assert.Equal(t, "example.com", q.Get("sni"))
	assert.Equal(t, "100 mbps", q.Get("up"))
	assert.Equal(t, "200 mbps", q.Get("down"))
}

func TestGenerate_Hy2_WithSalamanderBandwidthPortHopping(t *testing.T) {
	tls := &conf.TLSConfig{ServerName: "example.com"}

	fm := &conf.FinalMask{
		QuicParams: &conf.QuicParamsConfig{
			Congestion: "brutal",
			BrutalUp:   conf.Bandwidth("50 mbps"),
			BrutalDown: conf.Bandwidth("100 mbps"),
		},
	}

	salamander := &conf.Salamander{Password: "secret"}
	salJSON, err := json.Marshal(salamander)
	require.NoError(t, err)
	// xray-core 26.11.9: port hopping is the udphop UDP mask, last after salamander
	fm.Udp = []conf.Mask{
		{Type: "salamander", Settings: new(json.RawMessage(salJSON))},
		{Type: "udphop", Settings: hy2HopMaskSettings(t, "20000-40000", 30)},
	}

	ss := buildHy2StreamSettings("auth", tls, fm)
	outbound := buildHy2Outbound(t, "auth", "host", 443, ss)

	link, err := shareLink(outbound)
	require.NoError(t, err)

	q := link.Query()
	assert.Equal(t, "example.com", q.Get("sni"))
	assert.Equal(t, "50 mbps", q.Get("up"))
	assert.Equal(t, "100 mbps", q.Get("down"))
	assert.Equal(t, "20000-40000", q.Get("ports"))
	assert.Equal(t, "30", q.Get("hop-interval"))
	assert.Equal(t, "salamander", q.Get("obfs"))
	assert.Equal(t, "secret", q.Get("obfs-password"))
}

// A udphop mask without salamander must not turn into obfs=salamander with an empty password.
func TestGenerate_Hy2_HopWithoutSalamander(t *testing.T) {
	tls := &conf.TLSConfig{ServerName: "example.com"}
	fm := &conf.FinalMask{Udp: []conf.Mask{{Type: "udphop", Settings: hy2HopMaskSettings(t, "20000-40000", 10)}}}
	ss := buildHy2StreamSettings("auth", tls, fm)
	outbound := buildHy2Outbound(t, "auth", "host", 443, ss)

	link, err := shareLink(outbound)
	require.NoError(t, err)

	q := link.Query()
	assert.Equal(t, "20000-40000", q.Get("ports"))
	assert.Equal(t, "10", q.Get("hop-interval"))
	assert.False(t, q.Has("obfs"))
	assert.False(t, q.Has("obfs-password"))
}

func hy2HopMaskSettings(t *testing.T, ports string, interval int32) *json.RawMessage {
	t.Helper()
	var pl conf.PortList
	require.NoError(t, pl.UnmarshalJSON([]byte(`"`+ports+`"`)))
	raw, err := json.Marshal(&conf.UDPHop{
		Mode:        "intervalLocal,intervalRemote",
		RemotePorts: pl,
		Interval:    conf.Int32Range{Left: interval, Right: interval, From: interval, To: interval},
	})
	require.NoError(t, err)
	msg := json.RawMessage(raw)
	return &msg
}

func TestGenerate_Hy2_WithFullTLSParams(t *testing.T) {
	alpn := conf.StringList{"h3"}
	tls := &conf.TLSConfig{
		ServerName:  "example.com",
		Fingerprint: "chrome",
		ALPN:        &alpn,
	}
	ss := buildHy2StreamSettings("auth", tls, nil)
	outbound := buildHy2Outbound(t, "auth", "host", 443, ss)

	link, err := shareLink(outbound)
	require.NoError(t, err)

	q := link.Query()
	assert.Equal(t, "example.com", q.Get("sni"))
	assert.Equal(t, "chrome", q.Get("fp"))
	assert.Equal(t, "h3", q.Get("alpn"))
}

func TestGenerate_Hy2_RoundTrip(t *testing.T) {
	original := "hy2://auth@host:443?up=50+mbps&down=100+mbps&obfs=salamander&obfs-password=secret&ports=20000-40000&hop-interval=30&sni=example.com&alpn=h3&fp=chrome"

	config, err := ConvertShareLinksToXrayJson(original)
	require.NoError(t, err)
	require.Len(t, config.OutboundConfigs, 1)

	generated, err := shareLink(config.OutboundConfigs[0])
	require.NoError(t, err)

	// Parse both URLs to compare query params (order may differ)
	origURL, err := url.Parse(original)
	require.NoError(t, err)
	origQ := origURL.Query()
	genQ := generated.Query()

	assert.Equal(t, "hysteria2", generated.Scheme)
	assert.Equal(t, origQ.Get("sni"), genQ.Get("sni"))
	assert.Equal(t, origQ.Get("fp"), genQ.Get("fp"))
	assert.Equal(t, origQ.Get("alpn"), genQ.Get("alpn"))
	assert.Equal(t, origQ.Get("up"), genQ.Get("up"))
	assert.Equal(t, origQ.Get("down"), genQ.Get("down"))
	assert.Equal(t, origQ.Get("ports"), genQ.Get("ports"))
	assert.Equal(t, origQ.Get("hop-interval"), genQ.Get("hop-interval"))
	assert.Equal(t, origQ.Get("obfs"), genQ.Get("obfs"))
	assert.Equal(t, origQ.Get("obfs-password"), genQ.Get("obfs-password"))
}

// Links round-trip byte for byte as with the old QuicParams.UdpHop: the checker keys servers by the canonical link.
func TestGenerate_Hy2_HopRoundTripKeepsTheLink(t *testing.T) {
	cases := []struct {
		name, link, wantPorts, wantInterval string
	}{
		{"range, no interval", "hy2://auth@host:443?ports=20000-40000&sni=example.com", "20000-40000", ""},
		{"range, interval 0 = core default", "hy2://auth@host:443?ports=20000-40000&hop-interval=0&sni=example.com", "20000-40000", ""},
		{"range, interval 15", "hy2://auth@host:443?ports=20000-40000&hop-interval=15&sni=example.com", "20000-40000", "15"},
		{"single port", "hy2://auth@host:443?ports=8443&sni=example.com", "8443", ""},
		{"list", "hy2://auth@host:443?ports=8443,20000-20010&sni=example.com", "8443,20000-20010", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			config, err := ConvertShareLinksToXrayJson(c.link)
			require.NoError(t, err)
			require.Len(t, config.OutboundConfigs, 1)
			generated, err := shareLink(config.OutboundConfigs[0])
			require.NoError(t, err)
			q := generated.Query()
			assert.Equal(t, c.wantPorts, q.Get("ports"))
			assert.Equal(t, c.wantInterval, q.Get("hop-interval"))
			assert.False(t, q.Has("obfs"))
		})
	}
}

// A hop interval under 5 s is refused, as the core refuses it.
func TestHysteria2_HopIntervalBelowFiveRefused(t *testing.T) {
	_, err := buildHy2FinalMask("", "", "20000-40000", new(int32(3)), "", "")
	require.Error(t, err)
}

// The core itself builds the stream config of a hop link: mask types and settings it accepts.
func TestHysteria2_CoreBuildsHopMask(t *testing.T) {
	for _, link := range []string{
		"hy2://auth@host:443?obfs=salamander&obfs-password=secret&ports=20000-40000&hop-interval=10&sni=example.com",
		"hy2://auth@host:443?ports=8443&sni=example.com",
		"hy2://auth@host:443?up=50+mbps&down=100+mbps&ports=20000-40000&sni=example.com",
	} {
		outbound := parseHy2Link(t, link)
		require.NotNil(t, outbound.StreamSetting)
		_, err := outbound.StreamSetting.Build()
		require.NoError(t, err, link)
	}
}

func TestConvertXrayJsonToShareLinks_RoundTripProtocols(t *testing.T) {
	cases := []string{
		"vless://" + testShareUUID + "@r1.example:443?encryption=none&security=tls&sni=r1.example&type=ws&path=%2Fr&host=h.r1",
		"trojan://trpass@r2.example:443?sni=r2.example",
		"ss://" + ssUserB64("aes-128-gcm", "pw") + "@r3.example:8389",
		"vmess://" + testShareUUID + "@r4.example:443?encryption=none&type=tcp",
		"socks://" + base64.StdEncoding.EncodeToString([]byte("u:p")) + "@127.0.0.1:1090",
	}
	for _, link := range cases {
		t.Run(link[:12], func(t *testing.T) {
			cfg, err := ConvertShareLinksToXrayJson(link)
			require.NoError(t, err)
			out, err := json.Marshal(cfg)
			require.NoError(t, err)
			text, err := ConvertXrayJsonToShareLinks(out)
			require.NoError(t, err)
			assert.NotEmpty(t, text)
			again, err := ConvertShareLinksToXrayJson(text)
			require.NoError(t, err)
			require.Len(t, again.OutboundConfigs, 1)
			assert.Equal(t, cfg.OutboundConfigs[0].Protocol, again.OutboundConfigs[0].Protocol)
		})
	}
}

func TestConvertXrayJsonToShareLinks_Errors(t *testing.T) {
	_, err := ConvertXrayJsonToShareLinks([]byte(`{"outbounds":[]}`))
	require.Error(t, err)

	_, err = ConvertXrayJsonToShareLinks([]byte(`{`))
	require.Error(t, err)
}

func TestConvertXrayJsonToShareLinks_PrefersTagWhenSendThroughEmpty(t *testing.T) {
	cfg, err := ConvertShareLinksToXrayJson(`trojan://pw@tag.example:443`)
	require.NoError(t, err)
	ob := cfg.OutboundConfigs[0]
	empty := ""
	ob.SendThrough = &empty
	ob.Tag = "named-by-tag"
	out, err := json.Marshal(&conf.Config{OutboundConfigs: []conf.OutboundDetourConfig{ob}})
	require.NoError(t, err)
	links, err := ConvertXrayJsonToShareLinks(out)
	require.NoError(t, err)
	assert.Contains(t, links, "#named-by-tag")
}
