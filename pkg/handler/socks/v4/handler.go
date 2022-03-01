package v4

import (
	"context"
	"net"
	"time"

	"github.com/go-gost/gosocks4"
	"github.com/go-gost/gost/pkg/chain"
	"github.com/go-gost/gost/pkg/handler"
	"github.com/go-gost/gost/pkg/logger"
	md "github.com/go-gost/gost/pkg/metadata"
	"github.com/go-gost/gost/pkg/registry"
)

func init() {
	registry.HandlerRegistry().Register("socks4", NewHandler)
	registry.HandlerRegistry().Register("socks4a", NewHandler)
}

type socks4Handler struct {
	router  *chain.Router
	md      metadata
	options handler.Options
}

func NewHandler(opts ...handler.Option) handler.Handler {
	options := handler.Options{}
	for _, opt := range opts {
		opt(&options)
	}

	return &socks4Handler{
		options: options,
	}
}

func (h *socks4Handler) Init(md md.Metadata) (err error) {
	if err := h.parseMetadata(md); err != nil {
		return err
	}

	h.router = h.options.Router
	if h.router == nil {
		h.router = (&chain.Router{}).WithLogger(h.options.Logger)
	}

	return nil
}

func (h *socks4Handler) Handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	start := time.Now()

	log := h.options.Logger.WithFields(map[string]any{
		"remote": conn.RemoteAddr().String(),
		"local":  conn.LocalAddr().String(),
	})

	log.Infof("%s <> %s", conn.RemoteAddr(), conn.LocalAddr())
	defer func() {
		log.WithFields(map[string]any{
			"duration": time.Since(start),
		}).Infof("%s >< %s", conn.RemoteAddr(), conn.LocalAddr())
	}()

	if h.md.readTimeout > 0 {
		conn.SetReadDeadline(time.Now().Add(h.md.readTimeout))
	}

	req, err := gosocks4.ReadRequest(conn)
	if err != nil {
		log.Error(err)
		return
	}
	log.Debug(req)

	conn.SetReadDeadline(time.Time{})

	if h.options.Auther != nil &&
		!h.options.Auther.Authenticate(string(req.Userid), "") {
		resp := gosocks4.NewReply(gosocks4.RejectedUserid, nil)
		resp.Write(conn)
		log.Debug(resp)
		return
	}

	switch req.Cmd {
	case gosocks4.CmdConnect:
		h.handleConnect(ctx, conn, req, log)
	case gosocks4.CmdBind:
		h.handleBind(ctx, conn, req)
	default:
		log.Errorf("unknown cmd: %d", req.Cmd)
	}
}

func (h *socks4Handler) handleConnect(ctx context.Context, conn net.Conn, req *gosocks4.Request, log logger.Logger) {
	addr := req.Addr.String()

	log = log.WithFields(map[string]any{
		"dst": addr,
	})
	log.Infof("%s >> %s", conn.RemoteAddr(), addr)

	if h.options.Bypass != nil && h.options.Bypass.Contains(addr) {
		resp := gosocks4.NewReply(gosocks4.Rejected, nil)
		resp.Write(conn)
		log.Debug(resp)
		log.Info("bypass: ", addr)
		return
	}

	cc, err := h.router.Dial(ctx, "tcp", addr)
	if err != nil {
		resp := gosocks4.NewReply(gosocks4.Failed, nil)
		resp.Write(conn)
		log.Debug(resp)
		return
	}

	defer cc.Close()

	resp := gosocks4.NewReply(gosocks4.Granted, nil)
	if err := resp.Write(conn); err != nil {
		log.Error(err)
		return
	}
	log.Debug(resp)

	t := time.Now()
	log.Infof("%s <-> %s", conn.RemoteAddr(), addr)
	handler.Transport(conn, cc)
	log.WithFields(map[string]any{
		"duration": time.Since(t),
	}).Infof("%s >-< %s", conn.RemoteAddr(), addr)
}

func (h *socks4Handler) handleBind(ctx context.Context, conn net.Conn, req *gosocks4.Request) {
	// TODO: bind
}
