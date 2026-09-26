//go:build android || singboxtest

package configregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"golang.getoutline.org/sdk/network/packetrelay"
	"golang.getoutline.org/sdk/transport"
	"localhost/client/go/configyaml"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/block"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing-box/protocol/shadowsocks"
	"github.com/sagernet/sing-box/protocol/vless"
	sjson "github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
)

var singboxDNS = netip.MustParseAddrPort("169.254.113.53:53")
var singboxRemoteDNS = netip.MustParseAddrPort("1.1.1.1:53")

type singboxConfig struct {
	Config string
	// Location is informational and is not needed to establish the tunnel.
	Location string
}

type singboxRuntime struct {
	mu      sync.RWMutex
	ctx     context.Context
	options option.Options
	box     *box.Box
}

func newSingboxContext() context.Context {
	inbounds := inbound.NewRegistry()
	outbounds := outbound.NewRegistry()
	direct.RegisterOutbound(outbounds)
	block.RegisterOutbound(outbounds)
	shadowsocks.RegisterOutbound(outbounds)
	vless.RegisterOutbound(outbounds)
	group.RegisterURLTest(outbounds)
	dnsTransports := dns.NewTransportRegistry()
	local.RegisterTransport(dnsTransports)
	return box.Context(context.Background(), inbounds, outbounds,
		endpoint.NewRegistry(), dnsTransports, boxservice.NewRegistry())
}

func NewSingboxTransportPairSubParser() func(context.Context, map[string]any) (*TransportPair, error) {
	return func(_ context.Context, configMap map[string]any) (*TransportPair, error) {
		var config singboxConfig
		if err := configyaml.MapToAny(configMap, &config); err != nil {
			return nil, fmt.Errorf("invalid sing-box transport: %w", err)
		}
		if config.Config == "" {
			return nil, errors.New("sing-box config is empty")
		}
		if err := validateSingboxEnvelope(config.Config); err != nil {
			return nil, err
		}
		ctx := newSingboxContext()
		options, err := sjson.UnmarshalExtendedContext[option.Options](ctx, []byte(config.Config))
		if err != nil {
			return nil, fmt.Errorf("invalid sing-box config: %w", err)
		}
		if len(options.Inbounds) != 0 {
			return nil, errors.New("sing-box config must not contain inbounds")
		}
		if options.Route == nil || options.Route.Final == "" {
			return nil, errors.New("sing-box route.final is required")
		}
		firstHop, connectionType, err := singboxRouteInfo(config.Config, options.Route.Final)
		if err != nil {
			return nil, err
		}
		runtime := &singboxRuntime{ctx: ctx, options: options}
		stream := &Dialer[transport.StreamConn]{
			ConnectionProviderInfo: ConnectionProviderInfo{connectionType, firstHop},
			Dial:                   runtime.dialStream,
		}
		relay := &PacketRelay{
			ConnectionProviderInfo: ConnectionProviderInfo{connectionType, firstHop},
			PacketRelay:            &singboxPacketRelay{runtime: runtime},
			NotifyNetworkChanged:   func() {},
		}
		return &TransportPair{StreamDialer: stream, PacketRelay: relay,
			Start: runtime.start, Close: runtime.close}, nil
	}
}

func validateSingboxEnvelope(configText string) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(configText), &root); err != nil {
		return fmt.Errorf("invalid sing-box JSON: %w", err)
	}
	for key := range root {
		if key != "outbounds" && key != "route" {
			return fmt.Errorf("unsupported sing-box section: %s", key)
		}
	}
	var route map[string]json.RawMessage
	if err := json.Unmarshal(root["route"], &route); err != nil {
		return fmt.Errorf("invalid sing-box route: %w", err)
	}
	for key := range route {
		if key != "final" {
			return fmt.Errorf("unsupported sing-box route option: %s", key)
		}
	}
	return nil
}

func singboxRouteInfo(configText, final string) (string, ConnType, error) {
	var config struct {
		Outbounds []struct {
			Tag       string   `json:"tag"`
			Type      string   `json:"type"`
			Server    string   `json:"server"`
			Port      int      `json:"server_port"`
			Outbounds []string `json:"outbounds"`
		} `json:"outbounds"`
	}
	if err := sjson.Unmarshal([]byte(configText), &config); err != nil {
		return "", ConnTypeBlocked, err
	}
	byTag := make(map[string]int)
	for i, entry := range config.Outbounds {
		byTag[entry.Tag] = i
	}
	for range config.Outbounds {
		i, ok := byTag[final]
		if !ok {
			return "", ConnTypeBlocked, fmt.Errorf("sing-box outbound not found: %s", final)
		}
		entry := config.Outbounds[i]
		if entry.Type == "direct" {
			return "", ConnTypeDirect, nil
		}
		if entry.Type == "block" {
			return "", ConnTypeBlocked, nil
		}
		if entry.Server != "" && entry.Port > 0 && entry.Port <= 65535 {
			return net.JoinHostPort(entry.Server, fmt.Sprint(entry.Port)), ConnTypeTunneled, nil
		}
		if len(entry.Outbounds) == 0 {
			return "", ConnTypeBlocked, fmt.Errorf("sing-box outbound has no server: %s", final)
		}
		for _, tag := range entry.Outbounds {
			child, ok := byTag[tag]
			if !ok || config.Outbounds[child].Type == "direct" || config.Outbounds[child].Type == "block" {
				return "", ConnTypeBlocked, fmt.Errorf("sing-box group contains an unsupported route: %s", tag)
			}
		}
		final = entry.Outbounds[0]
	}
	return "", ConnTypeBlocked, errors.New("sing-box outbound cycle")
}

func (s *singboxRuntime) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.box != nil {
		return nil
	}
	instance, err := box.New(box.Options{Context: s.ctx, Options: s.options})
	if err != nil {
		return fmt.Errorf("create sing-box: %w", err)
	}
	if err := instance.Start(); err != nil {
		return fmt.Errorf("start sing-box: %w", err)
	}
	s.box = instance
	return nil
}

func (s *singboxRuntime) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.box == nil {
		return nil
	}
	err := s.box.Close()
	s.box = nil
	return err
}

func (s *singboxRuntime) defaultOutbound() (outboundDialer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.box == nil || s.box.Outbound().Default() == nil {
		return nil, errors.New("sing-box is not running")
	}
	return s.box.Outbound().Default(), nil
}

type outboundDialer interface {
	DialContext(context.Context, string, M.Socksaddr) (net.Conn, error)
	ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error)
}

func (s *singboxRuntime) dialStream(ctx context.Context, address string) (transport.StreamConn, error) {
	outbound, err := s.defaultOutbound()
	if err != nil {
		return nil, err
	}
	if address == singboxDNS.String() {
		address = singboxRemoteDNS.String()
	}
	conn, err := outbound.DialContext(ctx, "tcp", M.ParseSocksaddr(address))
	if err != nil {
		return nil, err
	}
	return &singboxStreamConn{Conn: conn}, nil
}

type singboxStreamConn struct{ net.Conn }

func (c *singboxStreamConn) CloseRead() error {
	if half, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return half.CloseRead()
	}
	return nil
}

func (c *singboxStreamConn) CloseWrite() error {
	if half, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return c.Conn.Close()
}

type singboxPacketRelay struct{ runtime *singboxRuntime }

func (r *singboxPacketRelay) NewAssociation() (packetrelay.PacketSender, packetrelay.PacketReceiver, error) {
	if _, err := r.runtime.defaultOutbound(); err != nil {
		return nil, nil, err
	}
	a := &singboxAssociation{
		runtime: r.runtime,
		conns:   make(map[netip.AddrPort]net.PacketConn),
		packets: make(chan singboxPacket, 32),
		done:    make(chan struct{}),
	}
	return a, a, nil
}

type singboxPacket struct {
	data   []byte
	source netip.AddrPort
}

type singboxAssociation struct {
	runtime *singboxRuntime
	mu      sync.Mutex
	conns   map[netip.AddrPort]net.PacketConn
	packets chan singboxPacket
	done    chan struct{}
	once    sync.Once
}

func (a *singboxAssociation) SendPacket(p []byte, destination netip.AddrPort) error {
	select {
	case <-a.done:
		return packetrelay.ErrClosed
	default:
	}
	actual := destination
	if destination == singboxDNS {
		actual = singboxRemoteDNS
	}
	a.mu.Lock()
	conn := a.conns[actual]
	if conn == nil {
		outbound, err := a.runtime.defaultOutbound()
		if err != nil {
			a.mu.Unlock()
			return err
		}
		conn, err = outbound.ListenPacket(context.Background(), M.SocksaddrFrom(actual.Addr(), actual.Port()))
		if err != nil {
			a.mu.Unlock()
			return err
		}
		a.conns[actual] = conn
		go a.readPackets(conn, actual)
	}
	a.mu.Unlock()
	_, err := conn.WriteTo(p, net.UDPAddrFromAddrPort(actual))
	return err
}

func (a *singboxAssociation) readPackets(conn net.PacketConn, destination netip.AddrPort) {
	buf := make([]byte, 65535)
	for {
		n, source, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		addrPort, err := netip.ParseAddrPort(source.String())
		if err != nil {
			addrPort = destination
		}
		if addrPort == singboxRemoteDNS {
			addrPort = singboxDNS
		}
		packet := singboxPacket{data: append([]byte(nil), buf[:n]...), source: addrPort}
		select {
		case a.packets <- packet:
		case <-a.done:
			return
		}
	}
}

func (a *singboxAssociation) ReceivePackets(handler packetrelay.PacketHandler) error {
	for {
		select {
		case packet := <-a.packets:
			if err := handler.HandlePacket(packet.data, packet.source); err != nil {
				return err
			}
		case <-a.done:
			return packetrelay.ErrClosed
		}
	}
}

func (a *singboxAssociation) Close() error {
	a.once.Do(func() {
		close(a.done)
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, conn := range a.conns {
			_ = conn.Close()
		}
	})
	return nil
}
