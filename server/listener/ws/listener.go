package tcp

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/go-gost/gost/logger"
	"github.com/go-gost/gost/server/listener"
	"github.com/go-gost/gost/utils"
	"github.com/gorilla/websocket"
)

var (
	_ listener.Listener = (*Listener)(nil)
)

type Listener struct {
	md       metadata
	addr     net.Addr
	upgrader *websocket.Upgrader
	srv      *http.Server
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

	l.upgrader = &websocket.Upgrader{
		HandshakeTimeout:  l.md.handshakeTimeout,
		ReadBufferSize:    l.md.readBufferSize,
		WriteBufferSize:   l.md.writeBufferSize,
		CheckOrigin:       func(r *http.Request) bool { return true },
		EnableCompression: l.md.enableCompression,
	}

	path := l.md.path
	if path == "" {
		path = defaultPath
	}
	mux := http.NewServeMux()
	mux.Handle(path, http.HandlerFunc(l.upgrade))
	l.srv = &http.Server{
		Addr:              l.md.addr,
		TLSConfig:         l.md.tlsConfig,
		Handler:           mux,
		ReadHeaderTimeout: l.md.readHeaderTimeout,
	}

	queueSize := l.md.connQueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	l.connChan = make(chan net.Conn, queueSize)
	l.errChan = make(chan error, 1)

	ln, err := net.Listen("tcp", l.md.addr)
	if err != nil {
		return
	}
	if l.md.tlsConfig != nil {
		ln = tls.NewListener(ln, l.md.tlsConfig)
	}

	l.addr = ln.Addr()

	go func() {
		err := l.srv.Serve(ln)
		if err != nil {
			l.errChan <- err
		}
		close(l.errChan)
	}()

	select {
	case err = <-l.errChan:
		return
	case <-time.After(100 * time.Millisecond):
	}

	return
}

func (l *Listener) Accept() (conn net.Conn, err error) {
	select {
	case conn = <-l.connChan:
	case err = <-l.errChan:
	}
	return
}

func (l *Listener) Close() error {
	return l.srv.Close()
}

func (l *Listener) Addr() net.Addr {
	return l.addr
}

func (l *Listener) parseMetadata(md listener.Metadata) (m metadata, err error) {
	if val, ok := md[addr]; ok {
		m.addr = val
	} else {
		err = errors.New("missing address")
		return
	}

	m.tlsConfig, err = utils.LoadTLSConfig(md[certFile], md[keyFile], md[caFile])
	if err != nil {
		return
	}

	return
}

func (l *Listener) upgrade(w http.ResponseWriter, r *http.Request) {
	conn, err := l.upgrader.Upgrade(w, r, l.md.responseHeader)
	if err != nil {
		l.logger.Error(err)
		return
	}

	select {
	case l.connChan <- &websocketConn{Conn: conn}:
	default:
		conn.Close()
		l.logger.Warn("connection queue is full")
	}
}

type websocketConn struct {
	*websocket.Conn
	rb []byte
}

func (c *websocketConn) Read(b []byte) (n int, err error) {
	if len(c.rb) == 0 {
		_, c.rb, err = c.ReadMessage()
	}
	n = copy(b, c.rb)
	c.rb = c.rb[n:]
	return
}

func (c *websocketConn) Write(b []byte) (n int, err error) {
	err = c.WriteMessage(websocket.BinaryMessage, b)
	n = len(b)
	return
}

func (c *websocketConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}
