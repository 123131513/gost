package ssh

import (
	"context"
	"errors"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/go-gost/gost/pkg/dialer"
	ssh_util "github.com/go-gost/gost/pkg/internal/util/ssh"
	"github.com/go-gost/gost/pkg/logger"
	md "github.com/go-gost/gost/pkg/metadata"
	"github.com/go-gost/gost/pkg/registry"
	"golang.org/x/crypto/ssh"
)

func init() {
	registry.RegisterDialer("sshd", NewDialer)
}

type forwardDialer struct {
	user         *url.Userinfo
	sessions     map[string]*sshSession
	sessionMutex sync.Mutex
	logger       logger.Logger
	md           metadata
}

func NewDialer(opts ...dialer.Option) dialer.Dialer {
	options := &dialer.Options{}
	for _, opt := range opts {
		opt(options)
	}

	return &forwardDialer{
		user:     options.User,
		sessions: make(map[string]*sshSession),
		logger:   options.Logger,
	}
}

func (d *forwardDialer) Init(md md.Metadata) (err error) {
	if err = d.parseMetadata(md); err != nil {
		return
	}

	return nil
}

// Multiplex implements dialer.Multiplexer interface.
func (d *forwardDialer) Multiplex() bool {
	return true
}

func (d *forwardDialer) Dial(ctx context.Context, addr string, opts ...dialer.DialOption) (conn net.Conn, err error) {
	var options dialer.DialOptions
	for _, opt := range opts {
		opt(&options)
	}

	d.sessionMutex.Lock()
	defer d.sessionMutex.Unlock()

	session, ok := d.sessions[addr]
	if session != nil && session.IsClosed() {
		delete(d.sessions, addr) // session is dead
		ok = false
	}
	if !ok {
		conn, err = d.dial(ctx, "tcp", addr, &options)
		if err != nil {
			return
		}

		session = &sshSession{
			addr: addr,
			conn: conn,
		}
		d.sessions[addr] = session
	}

	return session.conn, err
}

// Handshake implements dialer.Handshaker
func (d *forwardDialer) Handshake(ctx context.Context, conn net.Conn, options ...dialer.HandshakeOption) (net.Conn, error) {
	opts := &dialer.HandshakeOptions{}
	for _, option := range options {
		option(opts)
	}

	d.sessionMutex.Lock()
	defer d.sessionMutex.Unlock()

	if d.md.handshakeTimeout > 0 {
		conn.SetDeadline(time.Now().Add(d.md.handshakeTimeout))
		defer conn.SetDeadline(time.Time{})
	}

	session, ok := d.sessions[opts.Addr]
	if session != nil && session.conn != conn {
		err := errors.New("ssh: unrecognized connection")
		d.logger.Error(err)
		conn.Close()
		delete(d.sessions, opts.Addr)
		return nil, err
	}

	if !ok || session.client == nil {
		s, err := d.initSession(ctx, opts.Addr, conn)
		if err != nil {
			d.logger.Error(err)
			conn.Close()
			delete(d.sessions, opts.Addr)
			return nil, err
		}
		session = s
		go func() {
			s.wait()
			d.logger.Debug("session closed")
		}()
		d.sessions[opts.Addr] = session
	}
	if session.IsClosed() {
		delete(d.sessions, opts.Addr)
		return nil, ssh_util.ErrSessionDead
	}

	return ssh_util.NewClientConn(session.conn, session.client), nil
}

func (d *forwardDialer) dial(ctx context.Context, network, addr string, opts *dialer.DialOptions) (net.Conn, error) {
	dial := opts.DialFunc
	if dial != nil {
		conn, err := dial(ctx, addr)
		if err != nil {
			d.logger.Error(err)
		} else {
			d.logger.WithFields(map[string]interface{}{
				"src": conn.LocalAddr().String(),
				"dst": addr,
			}).Debug("dial with dial func")
		}
		return conn, err
	}

	var netd net.Dialer
	conn, err := netd.DialContext(ctx, network, addr)
	if err != nil {
		d.logger.Error(err)
	} else {
		d.logger.WithFields(map[string]interface{}{
			"src": conn.LocalAddr().String(),
			"dst": addr,
		}).Debugf("dial direct %s/%s", addr, network)
	}
	return conn, err
}

func (d *forwardDialer) initSession(ctx context.Context, addr string, conn net.Conn) (*sshSession, error) {
	config := ssh.ClientConfig{
		// Timeout:         timeout,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	if d.user != nil {
		config.User = d.user.Username()
		if password, _ := d.user.Password(); password != "" {
			config.Auth = []ssh.AuthMethod{
				ssh.Password(password),
			}
		}
	}
	if d.md.signer != nil {
		config.Auth = append(config.Auth, ssh.PublicKeys(d.md.signer))
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, &config)
	if err != nil {
		return nil, err
	}

	return &sshSession{
		conn:   conn,
		client: ssh.NewClient(sshConn, chans, reqs),
		closed: make(chan struct{}),
		dead:   make(chan struct{}),
	}, nil
}
