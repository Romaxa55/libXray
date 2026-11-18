package share

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MegaV: the core of xray-core.ref builds VLESS without TLS to a public address, as the fork did before
// (upstream refuses them since XTLS/Xray-core#6303; the checker has to keep telling such working servers from dead
// ones). A core without that patch fails here. Trojan is not here: a trojan:// link always gets TLS (stream.go).
func TestMegaVPlaintextShareLinksBuild(t *testing.T) {
	for _, link := range []string{
		"vless://" + testShareUUID + "@1.1.1.1:80?type=ws&security=none&encryption=none&path=%2F#ws",
		"vless://" + testShareUUID + "@megav.app:8080?type=tcp&encryption=none#raw",
	} {
		config, err := ConvertShareLinksToXrayJson(link)
		require.NoError(t, err, link)
		require.Len(t, config.OutboundConfigs, 1, link)
		// The parser keeps the link's name in sendThrough; the clients drop it before they build.
		ob := config.OutboundConfigs[0]
		ob.SendThrough = nil
		_, err = ob.Build()
		assert.NoError(t, err, link)
	}
}
