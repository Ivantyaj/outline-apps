//go:build singboxtest

package configregistry

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"localhost/client/go/configyaml"
)

func TestSingboxVLESSRealityURLTest(t *testing.T) {
	config, err := configyaml.ParseConfigYAML(`
$type: singbox
location: Test
config: |
  {
    "outbounds": [
      {"type":"shadowsocks","tag":"ss1","server":"127.0.0.1","server_port":1,"method":"chacha20-ietf-poly1305","password":"test","network":"tcp"},
      {"type":"vless","tag":"node-vless","server":"127.0.0.1","server_port":443,"uuid":"00000000-0000-0000-0000-000000000000","flow":"xtls-rprx-vision","tls":{"enabled":true,"server_name":"example.com","utls":{"enabled":true,"fingerprint":"firefox"},"reality":{"enabled":true,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","short_id":"88debc420eff0013"}}},
      {"type":"urltest","tag":"auto","outbounds":["node-vless"],"url":"http://example.com/generate_204","interval":"45s","tolerance":200},
      {"type":"direct","tag":"direct"},
      {"type":"block","tag":"block"}
    ],
    "route":{"final":"auto"}
  }`)
	require.NoError(t, err)

	pair, err := newTestTransportProvider().Parse(context.Background(), config)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:443", pair.StreamDialer.FirstHop)
	require.Equal(t, ConnTypeTunneled, pair.StreamDialer.ConnType)
	require.Equal(t, ConnTypeTunneled, pair.PacketRelay.ConnType)
	require.NoError(t, pair.Start())
	require.NoError(t, pair.Close())
}

type capturePacket struct {
	packets chan []byte
}

func (c capturePacket) HandlePacket(p []byte, _ netip.AddrPort) error {
	c.packets <- append([]byte(nil), p...)
	return nil
}

func TestSingboxUDPAssociation(t *testing.T) {
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echo.Close()
	go func() {
		buf := make([]byte, 1024)
		n, addr, readErr := echo.ReadFrom(buf)
		if readErr == nil {
			_, _ = echo.WriteTo(buf[:n], addr)
		}
	}()

	config, err := configyaml.ParseConfigYAML(`$type: singbox
config: |
  {"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`)
	require.NoError(t, err)
	pair, err := newTestTransportProvider().Parse(context.Background(), config)
	require.NoError(t, err)
	require.NoError(t, pair.Start())
	defer pair.Close()
	sender, receiver, err := pair.PacketRelay.NewAssociation()
	require.NoError(t, err)
	defer sender.Close()
	response := capturePacket{packets: make(chan []byte, 1)}
	go func() { _ = receiver.ReceivePackets(response) }()
	destination := echo.LocalAddr().(*net.UDPAddr).AddrPort()
	require.NoError(t, sender.SendPacket([]byte("hello"), destination))
	select {
	case packet := <-response.packets:
		require.Equal(t, "hello", string(packet))
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for UDP response")
	}
}

func TestSingboxRejectsUnsupportedRouting(t *testing.T) {
	config, err := configyaml.ParseConfigYAML(`$type: singbox
config: |
  {"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct","rules":[{"domain":"example.com","outbound":"direct"}]}}`)
	require.NoError(t, err)
	_, err = newTestTransportProvider().Parse(context.Background(), config)
	require.ErrorContains(t, err, "unsupported sing-box route option: rules")
}
