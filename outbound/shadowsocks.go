package outbound

import (
	"context"
	"net"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/mux"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-shadowsocks"
	"github.com/sagernet/sing-shadowsocks/shadowimpl"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/uot"
)

var _ adapter.Outbound = (*Shadowsocks)(nil)

type Shadowsocks struct {
	myOutboundAdapter
	dialer          N.Dialer
	method          shadowsocks.Method
	serverAddr      M.Socksaddr
	uot             bool
	multiplexDialer N.Dialer
}

func NewShadowsocks(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.ShadowsocksOutboundOptions) (*Shadowsocks, error) {
	method, err := shadowimpl.FetchMethod(options.Method, options.Password)
	if err != nil {
		return nil, err
	}
	outbound := &Shadowsocks{
		myOutboundAdapter: myOutboundAdapter{
			protocol: C.TypeShadowsocks,
			network:  options.Network.Build(),
			router:   router,
			logger:   logger,
			tag:      tag,
		},
		dialer:     dialer.NewOutbound(router, options.OutboundDialerOptions),
		method:     method,
		serverAddr: options.ServerOptions.Build(),
		uot:        options.UoT,
	}
	if !options.UoT {
		outbound.multiplexDialer, err = mux.NewClientWithOptions(ctx, (*shadowsocksDialer)(outbound), common.PtrValueOrDefault(options.MultiplexOptions))
		if err != nil {
			return nil, err
		}
	}
	return outbound, nil
}

func (h *Shadowsocks) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.AppendContext(ctx)
	metadata.Outbound = h.tag
	metadata.Destination = destination
	if h.multiplexDialer == nil {
		switch N.NetworkName(network) {
		case N.NetworkTCP:
			h.logger.InfoContext(ctx, "outbound connection to ", destination)
		case N.NetworkUDP:
			if h.uot {
				h.logger.InfoContext(ctx, "outbound UoT packet connection to ", destination)
				tcpConn, err := (*shadowsocksDialer)(h).DialContext(ctx, N.NetworkTCP, M.Socksaddr{
					Fqdn: uot.UOTMagicAddress,
					Port: destination.Port,
				})
				if err != nil {
					return nil, err
				}
				return uot.NewClientConn(tcpConn), nil
			}
			h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
		}
		return (*shadowsocksDialer)(h).DialContext(ctx, network, destination)
	} else {
		switch N.NetworkName(network) {
		case N.NetworkTCP:
			h.logger.InfoContext(ctx, "outbound multiplex connection to ", destination)
		case N.NetworkUDP:
			h.logger.InfoContext(ctx, "outbound multiplex packet connection to ", destination)
		}
		return h.multiplexDialer.DialContext(ctx, network, destination)
	}
}

func (h *Shadowsocks) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, metadata := adapter.AppendContext(ctx)
	metadata.Outbound = h.tag
	metadata.Destination = destination
	if h.multiplexDialer == nil {
		if h.uot {
			h.logger.InfoContext(ctx, "outbound UoT packet connection to ", destination)
			tcpConn, err := (*shadowsocksDialer)(h).DialContext(ctx, N.NetworkTCP, M.Socksaddr{
				Fqdn: uot.UOTMagicAddress,
				Port: destination.Port,
			})
			if err != nil {
				return nil, err
			}
			return uot.NewClientConn(tcpConn), nil
		}
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
		return (*shadowsocksDialer)(h).ListenPacket(ctx, destination)
	} else {
		h.logger.InfoContext(ctx, "outbound multiplex packet connection to ", destination)
		return h.multiplexDialer.ListenPacket(ctx, destination)
	}
}

func (h *Shadowsocks) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext) error {
	return NewEarlyConnection(ctx, h, conn, metadata)
}

func (h *Shadowsocks) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext) error {
	return NewPacketConnection(ctx, h, conn, metadata)
}

func (h *Shadowsocks) Close() error {
	return common.Close(h.multiplexDialer)
}

var _ N.Dialer = (*shadowsocksDialer)(nil)

type shadowsocksDialer Shadowsocks

func (h *shadowsocksDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		outConn, err := h.dialer.DialContext(ctx, N.NetworkTCP, h.serverAddr)
		if err != nil {
			return nil, err
		}
		return h.method.DialEarlyConn(outConn, destination), nil
	case N.NetworkUDP:
		outConn, err := h.dialer.DialContext(ctx, N.NetworkUDP, h.serverAddr)
		if err != nil {
			return nil, err
		}
		return &bufio.BindPacketConn{PacketConn: h.method.DialPacketConn(outConn), Addr: destination}, nil
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (h *shadowsocksDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	outConn, err := h.dialer.DialContext(ctx, N.NetworkUDP, h.serverAddr)
	if err != nil {
		return nil, err
	}
	return h.method.DialPacketConn(outConn), nil
}
