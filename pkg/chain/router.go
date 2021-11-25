package chain

import (
	"bytes"
	"context"
	"fmt"
	"net"

	"github.com/go-gost/gost/pkg/connector"
	"github.com/go-gost/gost/pkg/logger"
)

type Router struct {
	chain   *Chain
	retries int
	logger  logger.Logger
}

func (r *Router) WithChain(chain *Chain) *Router {
	r.chain = chain
	return r
}

func (r *Router) WithRetry(retries int) *Router {
	r.retries = retries
	return r
}

func (r *Router) WithLogger(logger logger.Logger) *Router {
	r.logger = logger
	return r
}

func (r *Router) Dial(ctx context.Context, network, address string) (conn net.Conn, err error) {
	count := r.retries + 1
	if count <= 0 {
		count = 1
	}
	r.logger.Debugf("dial %s/%s", address, network)

	for i := 0; i < count; i++ {
		route := r.chain.GetRouteFor(network, address)

		if r.logger.IsLevelEnabled(logger.DebugLevel) {
			buf := bytes.Buffer{}
			for _, node := range route.Path() {
				fmt.Fprintf(&buf, "%s@%s > ", node.Name(), node.Addr())
			}
			fmt.Fprintf(&buf, "%s", address)
			r.logger.Debugf("route(retry=%d) %s", i, buf.String())
		}

		conn, err = route.Dial(ctx, network, address)
		if err == nil {
			break
		}
		r.logger.Errorf("route(retry=%d) %s", i, err)
	}

	return
}

func (r *Router) Bind(ctx context.Context, network, address string, opts ...connector.BindOption) (ln net.Listener, err error) {
	count := r.retries + 1
	if count <= 0 {
		count = 1
	}
	r.logger.Debugf("bind on %s/%s", address, network)

	for i := 0; i < count; i++ {
		route := r.chain.GetRouteFor(network, address)

		if r.logger.IsLevelEnabled(logger.DebugLevel) {
			buf := bytes.Buffer{}
			for _, node := range route.Path() {
				fmt.Fprintf(&buf, "%s@%s > ", node.Name(), node.Addr())
			}
			fmt.Fprintf(&buf, "%s", address)
			r.logger.Debugf("route(retry=%d) %s", i, buf.String())
		}

		ln, err = route.Bind(ctx, network, address, opts...)
		if err == nil {
			break
		}
		r.logger.Errorf("route(retry=%d) %s", i, err)
	}

	return
}
