package tls

import (
	"net"

	"github.com/go-gost/gost/pkg/components/internal/utils"
	"github.com/go-gost/gost/pkg/components/listener"
	md "github.com/go-gost/gost/pkg/components/metadata"
	"github.com/go-gost/gost/pkg/logger"
	"github.com/go-gost/gost/pkg/registry"
)

func init() {
	registry.RegisterListener("obfs-tls", NewListener)
}

type Listener struct {
	addr string
	md   metadata
	net.Listener
	logger logger.Logger
}

func NewListener(opts ...listener.Option) listener.Listener {
	options := &listener.Options{}
	for _, opt := range opts {
		opt(options)
	}
	return &Listener{
		addr:   options.Addr,
		logger: options.Logger,
	}
}

func (l *Listener) Init(md md.Metadata) (err error) {
	if err = l.parseMetadata(md); err != nil {
		return
	}

	laddr, err := net.ResolveTCPAddr("tcp", l.addr)
	if err != nil {
		return
	}
	ln, err := net.ListenTCP("tcp", laddr)
	if err != nil {
		return
	}

	if l.md.keepAlive {
		l.Listener = &utils.TCPKeepAliveListener{
			TCPListener:     ln,
			KeepAlivePeriod: l.md.keepAlivePeriod,
		}
		return
	}

	l.Listener = ln
	return
}

func (l *Listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return &conn{Conn: c}, nil
}

func (l *Listener) parseMetadata(md md.Metadata) (err error) {
	l.md.keepAlive = md.GetBool(keepAlive)
	l.md.keepAlivePeriod = md.GetDuration(keepAlivePeriod)

	return
}
