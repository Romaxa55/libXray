package share

import (
	"encoding/json"
	"fmt"

	"github.com/xtls/xray-core/infra/conf"
)

// buildHy2FinalMask builds Hysteria2 QUIC hop / bandwidth / salamander mask (shared by URI and Clash).
//
// Since xray-core 26.11.9 port hopping is the "udphop" UDP mask, not QuicParams.UdpHop. As in upstream libXray
// (share/hysteria2.go) the mode is "intervalLocal,intervalRemote" and the mask goes last, because the core wraps UDP
// masks in reverse order and hopping must wrap the raw socket. Unlike upstream, the config keeps what the link says
// and nothing more, so a link round-trips byte for byte as with the old QuicParams.UdpHop (the checker keys servers
// by the canonical link): a single port in "ports" is a hop list of one (the old core dialled it too), and the
// interval is set only when the link gives one — 0 or none leaves it to the core (30 s).
func buildHy2FinalMask(up, down, ports string, hopInterval *int32, obfsType, obfsPassword string) (*conf.FinalMask, error) {
	var quicParams *conf.QuicParamsConfig
	if up != "" || down != "" {
		quicParams = &conf.QuicParamsConfig{Congestion: "brutal"}
		if up != "" {
			quicParams.BrutalUp = conf.Bandwidth(up)
		}
		if down != "" {
			quicParams.BrutalDown = conf.Bandwidth(down)
		}
	}

	var udpMasks []conf.Mask
	if obfsType == "salamander" && obfsPassword != "" {
		salamanderRawMessage, err := convertJsonToRawMessage(&conf.Salamander{Password: obfsPassword})
		if err != nil {
			return nil, err
		}
		udpMasks = append(udpMasks, conf.Mask{Type: "salamander", Settings: &salamanderRawMessage})
	}

	if ports != "" {
		var portList conf.PortList
		portsJSON, err := json.Marshal(ports)
		if err != nil {
			return nil, err
		}
		if err := portList.UnmarshalJSON(portsJSON); err != nil {
			return nil, err
		}
		if len(portList.Range) > 0 {
			hop := &conf.UDPHop{Mode: "intervalLocal,intervalRemote", RemotePorts: portList}
			if hopInterval != nil && *hopInterval != 0 {
				interval := *hopInterval
				if interval < 5 {
					return nil, fmt.Errorf("hysteria2 hop interval %d is below 5 s", interval)
				}
				hop.Interval = conf.Int32Range{Left: interval, Right: interval, From: interval, To: interval}
			}
			hopRawMessage, err := convertJsonToRawMessage(hop)
			if err != nil {
				return nil, err
			}
			udpMasks = append(udpMasks, conf.Mask{Type: "udphop", Settings: &hopRawMessage})
		}
	}

	if quicParams == nil && len(udpMasks) == 0 {
		return nil, nil
	}
	return &conf.FinalMask{QuicParams: quicParams, Udp: udpMasks}, nil
}

// hy2HopFromFinalMask returns the ports and the interval of the udphop mask, if there is one. The interval is 0
// when the mask leaves it to the core or gives a range (a link carries one value).
func hy2HopFromFinalMask(mask *conf.FinalMask) (ports string, interval int32, ok bool) {
	if mask == nil {
		return "", 0, false
	}
	for _, entry := range mask.Udp {
		if entry.Type != "udphop" || entry.Settings == nil {
			continue
		}
		var hop conf.UDPHop
		if err := json.Unmarshal(*entry.Settings, &hop); err != nil || len(hop.RemotePorts.Range) == 0 {
			continue
		}
		if hop.Interval.From != hop.Interval.To {
			return hop.RemotePorts.String(), 0, true
		}
		return hop.RemotePorts.String(), hop.Interval.From, true
	}
	return "", 0, false
}

// hy2SalamanderFromFinalMask returns the salamander password, if there is such a mask.
func hy2SalamanderFromFinalMask(mask *conf.FinalMask) (password string, ok bool) {
	if mask == nil {
		return "", false
	}
	for _, entry := range mask.Udp {
		if entry.Type != "salamander" || entry.Settings == nil {
			continue
		}
		var obfs conf.Salamander
		if err := json.Unmarshal(*entry.Settings, &obfs); err != nil || obfs.Password == "" {
			continue
		}
		return obfs.Password, true
	}
	return "", false
}
