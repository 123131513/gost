package kcp

import (
	"errors"
	"net"
	"time"

	"github.com/go-gost/gost/logger"
	"github.com/go-gost/gost/server/listener"
	"github.com/go-gost/gost/utils"
	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"
	"github.com/xtaci/tcpraw"
)

var (
	_ listener.Listener = (*Listener)(nil)
)

type Listener struct {
	md       metadata
	ln       *kcp.Listener
	connChan chan net.Conn
	errChan  chan error
	logger   logger.Logger
}

func NewListener(opts ...listener.Option) *Listener {
	options := &listener.Options{}
	for _, opt := range opts {
		opt(options)
	}
	return &Listener{
		logger: options.Logger,
	}
}

func (l *Listener) Init(md listener.Metadata) (err error) {
	l.md, err = l.parseMetadata(md)
	if err != nil {
		return
	}

	config := l.md.config
	if config == nil {
		config = DefaultConfig
	}
	config.Init()

	if config.TCP {
		var conn net.PacketConn
		conn, err = tcpraw.Listen("tcp", addr)
		if err != nil {
			return
		}
		l.ln, err = kcp.ServeConn(
			blockCrypt(config.Key, config.Crypt, Salt), config.DataShard, config.ParityShard, conn)
	} else {
		l.ln, err = kcp.ListenWithOptions(addr,
			blockCrypt(config.Key, config.Crypt, Salt), config.DataShard, config.ParityShard)
	}
	if err != nil {
		return
	}

	if config.DSCP > 0 {
		if err = l.ln.SetDSCP(config.DSCP); err != nil {
			l.logger.Warn(err)
		}
	}
	if err = l.ln.SetReadBuffer(config.SockBuf); err != nil {
		l.logger.Warn(err)
	}
	if err = l.ln.SetWriteBuffer(config.SockBuf); err != nil {
		l.logger.Warn(err)
	}

	queueSize := l.md.connQueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	l.connChan = make(chan net.Conn, queueSize)
	l.errChan = make(chan error, 1)

	go l.listenLoop()

	return
}

func (l *Listener) Accept() (conn net.Conn, err error) {
	var ok bool
	select {
	case conn = <-l.connChan:
	case err, ok = <-l.errChan:
		if !ok {
			err = listener.ErrClosed
		}
	}
	return
}

func (l *Listener) Close() error {
	return l.ln.Close()
}

func (l *Listener) Addr() net.Addr {
	return l.ln.Addr()
}

func (l *Listener) listenLoop() {
	for {
		conn, err := l.ln.AcceptKCP()
		if err != nil {
			l.logger.Error("accept:", err)
			l.errChan <- err
			close(l.errChan)
			return
		}

		conn.SetStreamMode(true)
		conn.SetWriteDelay(false)
		conn.SetNoDelay(
			l.md.config.NoDelay,
			l.md.config.Interval,
			l.md.config.Resend,
			l.md.config.NoCongestion,
		)
		conn.SetMtu(l.md.config.MTU)
		conn.SetWindowSize(l.md.config.SndWnd, l.md.config.RcvWnd)
		conn.SetACKNoDelay(l.md.config.AckNodelay)
		go l.mux(conn)
	}
}

func (l *Listener) mux(conn net.Conn) {
	smuxConfig := smux.DefaultConfig()
	smuxConfig.MaxReceiveBuffer = l.md.config.SockBuf
	smuxConfig.KeepAliveInterval = time.Duration(l.md.config.KeepAlive) * time.Second

	if !l.md.config.NoComp {
		conn = utils.KCPCompStreamConn(conn)
	}

	mux, err := smux.Server(conn, smuxConfig)
	if err != nil {
		l.logger.Error(err)
		return
	}
	defer mux.Close()

	for {
		stream, err := mux.AcceptStream()
		if err != nil {
			l.logger.Error("accept stream:", err)
			return
		}

		select {
		case l.connChan <- stream:
		case <-stream.GetDieCh():
		default:
			stream.Close()
			l.logger.Error("connection queue is full")
		}
	}
}

func (l *Listener) parseMetadata(md listener.Metadata) (m metadata, err error) {
	if val, ok := md[addr]; ok {
		m.addr = val
	} else {
		err = errors.New("missing address")
		return
	}

	return
}
