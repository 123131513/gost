package udp

import (
	"context"
	"net"

	"github.com/go-gost/gost/pkg/dialer"
	"github.com/go-gost/gost/pkg/logger"
	md "github.com/go-gost/gost/pkg/metadata"
	"github.com/go-gost/gost/pkg/registry"
)

func init() {
	registry.DialerRegistry().Register("udp", NewDialer)
}

type udpDialer struct {
	md     metadata
	logger logger.Logger
}

func NewDialer(opts ...dialer.Option) dialer.Dialer {
	options := &dialer.Options{}
	for _, opt := range opts {
		opt(options)
	}

	return &udpDialer{
		logger: options.Logger,
	}
}

func (d *udpDialer) Init(md md.Metadata) (err error) {
	return d.parseMetadata(md)
}

func (d *udpDialer) Dial(ctx context.Context, addr string, opts ...dialer.DialOption) (net.Conn, error) {
	var options dialer.DialOptions
	for _, opt := range opts {
		opt(&options)
	}

	netd := options.NetDialer
	if netd == nil {
		netd = dialer.DefaultNetDialer
	}
	c, err := netd.Dial(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	return &conn{
		UDPConn: c.(*net.UDPConn),
	}, nil
}
