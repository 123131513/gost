package forward

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/go-gost/gost/pkg/bypass"
	"github.com/go-gost/gost/pkg/chain"
	"github.com/go-gost/gost/pkg/handler"
	"github.com/go-gost/gost/pkg/logger"
	md "github.com/go-gost/gost/pkg/metadata"
	"github.com/go-gost/gost/pkg/registry"
)

func init() {
	registry.RegisterHandler("tcp", NewHandler)
	registry.RegisterHandler("udp", NewHandler)
}

type forwardHandler struct {
	group  *chain.NodeGroup
	chain  *chain.Chain
	bypass bypass.Bypass
	logger logger.Logger
	md     metadata
}

func NewHandler(opts ...handler.Option) handler.Handler {
	options := &handler.Options{}
	for _, opt := range opts {
		opt(options)
	}

	return &forwardHandler{
		bypass: options.Bypass,
		logger: options.Logger,
	}
}

func (h *forwardHandler) Init(md md.Metadata) (err error) {
	return h.parseMetadata(md)
}

// WithChain implements chain.Chainable interface
func (h *forwardHandler) WithChain(chain *chain.Chain) {
	h.chain = chain
}

// Forward implements handler.Forwarder.
func (h *forwardHandler) Forward(group *chain.NodeGroup) {
	h.group = group
}

func (h *forwardHandler) Handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	start := time.Now()
	h.logger = h.logger.WithFields(map[string]interface{}{
		"remote": conn.RemoteAddr().String(),
		"local":  conn.LocalAddr().String(),
	})

	h.logger.Infof("%s <> %s", conn.RemoteAddr(), conn.LocalAddr())
	defer func() {
		h.logger.WithFields(map[string]interface{}{
			"duration": time.Since(start),
		}).Infof("%s >< %s", conn.RemoteAddr(), conn.LocalAddr())
	}()

	target := h.group.Next()
	if target == nil {
		h.logger.Error("no target available")
		return
	}

	network := "tcp"
	if _, ok := conn.(net.PacketConn); ok {
		network = "udp"
	}

	h.logger = h.logger.WithFields(map[string]interface{}{
		"dst": fmt.Sprintf("%s/%s", target.Addr(), network),
	})

	h.logger.Infof("%s >> %s", conn.RemoteAddr(), target.Addr())

	r := (&chain.Router{}).
		WithChain(h.chain).
		WithRetry(h.md.retryCount).
		WithLogger(h.logger)

	cc, err := r.Dial(ctx, network, target.Addr())
	if err != nil {
		h.logger.Error(err)
		// TODO: the router itself may be failed due to the failed node in the router,
		// the dead marker may be a wrong operation.
		target.Marker().Mark()
		return
	}
	defer cc.Close()
	target.Marker().Reset()

	t := time.Now()
	h.logger.Infof("%s <-> %s", conn.RemoteAddr(), target.Addr())
	handler.Transport(conn, cc)
	h.logger.
		WithFields(map[string]interface{}{
			"duration": time.Since(t),
		}).
		Infof("%s >-< %s", conn.RemoteAddr(), target.Addr())
}
