package v5

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-gost/gosocks5"
	"github.com/go-gost/gost/pkg/connector"
	"github.com/go-gost/gost/pkg/internal/utils/socks"
	"github.com/go-gost/gost/pkg/logger"
	md "github.com/go-gost/gost/pkg/metadata"
	"github.com/go-gost/gost/pkg/registry"
)

func init() {
	registry.RegiserConnector("socks5", NewConnector)
	registry.RegiserConnector("socks", NewConnector)
}

type socks5Connector struct {
	selector gosocks5.Selector
	logger   logger.Logger
	md       metadata
}

func NewConnector(opts ...connector.Option) connector.Connector {
	options := &connector.Options{}
	for _, opt := range opts {
		opt(options)
	}

	return &socks5Connector{
		logger: options.Logger,
	}
}

func (c *socks5Connector) Init(md md.Metadata) (err error) {
	if err = c.parseMetadata(md); err != nil {
		return
	}

	selector := &clientSelector{
		methods: []uint8{
			gosocks5.MethodNoAuth,
			gosocks5.MethodUserPass,
		},
		logger:    c.logger,
		User:      c.md.User,
		TLSConfig: c.md.tlsConfig,
	}
	if !c.md.noTLS {
		selector.methods = append(selector.methods, socks.MethodTLS)
		if selector.TLSConfig == nil {
			selector.TLSConfig = &tls.Config{
				InsecureSkipVerify: true,
			}
		}
	}
	c.selector = selector

	return
}

func (c *socks5Connector) Handshake(ctx context.Context, conn net.Conn) (net.Conn, error) {
	c.logger = c.logger.WithFields(map[string]interface{}{
		"remote": conn.RemoteAddr().String(),
		"local":  conn.LocalAddr().String(),
	})

	if c.md.connectTimeout > 0 {
		conn.SetDeadline(time.Now().Add(c.md.connectTimeout))
		defer conn.SetDeadline(time.Time{})
	}

	cc := gosocks5.ClientConn(conn, c.selector)
	if err := cc.Handleshake(); err != nil {
		return nil, err
	}

	return cc, nil
}

func (c *socks5Connector) Connect(ctx context.Context, conn net.Conn, network, address string, opts ...connector.ConnectOption) (net.Conn, error) {
	c.logger = c.logger.WithFields(map[string]interface{}{
		"target": address,
	})
	c.logger.Info("connect: ", address)

	addr, err := gosocks5.NewAddr(address)
	if err != nil {
		c.logger.Error(err)
		return nil, err
	}

	if c.md.connectTimeout > 0 {
		conn.SetDeadline(time.Now().Add(c.md.connectTimeout))
		defer conn.SetDeadline(time.Time{})
	}

	req := gosocks5.NewRequest(gosocks5.CmdConnect, addr)
	if err := req.Write(conn); err != nil {
		c.logger.Error(err)
		return nil, err
	}

	if c.logger.IsLevelEnabled(logger.DebugLevel) {
		c.logger.Debug(req)
	}

	reply, err := gosocks5.ReadReply(conn)
	if err != nil {
		c.logger.Error(err)
		return nil, err
	}

	if c.logger.IsLevelEnabled(logger.DebugLevel) {
		c.logger.Debug(reply)
	}

	if reply.Rep != gosocks5.Succeeded {
		return nil, errors.New("service unavailable")
	}

	return conn, nil
}

func (c *socks5Connector) parseMetadata(md md.Metadata) (err error) {
	if v := md.GetString(auth); v != "" {
		ss := strings.SplitN(v, ":", 2)
		if len(ss) == 1 {
			c.md.User = url.User(ss[0])
		} else {
			c.md.User = url.UserPassword(ss[0], ss[1])
		}
	}

	c.md.connectTimeout = md.GetDuration(connectTimeout)
	c.md.noTLS = md.GetBool(noTLS)

	return
}
