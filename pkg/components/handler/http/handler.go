package http

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-gost/gost/pkg/auth"
	"github.com/go-gost/gost/pkg/chain"
	"github.com/go-gost/gost/pkg/components/handler"
	md "github.com/go-gost/gost/pkg/components/metadata"
	"github.com/go-gost/gost/pkg/logger"
	"github.com/go-gost/gost/pkg/registry"
)

func init() {
	registry.RegisterHandler("http", NewHandler)
}

type Handler struct {
	chain  *chain.Chain
	logger logger.Logger
	md     metadata
}

func NewHandler(opts ...handler.Option) handler.Handler {
	options := &handler.Options{}
	for _, opt := range opts {
		opt(options)
	}

	return &Handler{
		chain:  options.Chain,
		logger: options.Logger,
	}
}

func (h *Handler) Init(md md.Metadata) error {
	return h.parseMetadata(md)
}

func (h *Handler) parseMetadata(md md.Metadata) error {
	h.md.proxyAgent = md.GetString(proxyAgentKey)

	if v, _ := md.Get(authsKey).([]interface{}); len(v) > 0 {
		authenticator := auth.NewLocalAuthenticator(nil)
		for _, auth := range v {
			if s, _ := auth.(string); s != "" {
				ss := strings.SplitN(s, ":", 2)
				if len(ss) == 1 {
					authenticator.Add(ss[0], "")
				} else {
					authenticator.Add(ss[0], ss[1])
				}
			}
		}
		h.md.authenticator = authenticator
	}

	if v := md.GetString(probeResistKey); v != "" {
		if ss := strings.SplitN(v, ":", 2); len(ss) == 2 {
			h.md.probeResist = &probeResist{
				Type:  ss[0],
				Value: ss[1],
				Knock: md.GetString(knockKey),
			}
		}
	}
	h.md.retryCount = md.GetInt(retryCount)

	return nil
}

func (h *Handler) Handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	h.logger = h.logger.WithFields(map[string]interface{}{
		"src":   conn.RemoteAddr().String(),
		"local": conn.LocalAddr().String(),
	})

	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		h.logger.Error(err)
		return
	}
	defer req.Body.Close()

	h.handleRequest(ctx, conn, req)
}

func (h *Handler) handleRequest(ctx context.Context, conn net.Conn, req *http.Request) {
	if req == nil {
		return
	}

	// try to get the actual host.
	if v := req.Header.Get("Gost-Target"); v != "" {
		if h, err := h.decodeServerName(v); err == nil {
			req.Host = h
		}
	}
	req.Header.Del("Gost-Target")

	host := req.Host
	if _, port, _ := net.SplitHostPort(host); port == "" {
		host = net.JoinHostPort(host, "80")
	}

	fields := map[string]interface{}{
		"dst": host,
	}
	if u, _, _ := h.basicProxyAuth(req.Header.Get("Proxy-Authorization")); u != "" {
		fields["user"] = u
	}
	h.logger = h.logger.WithFields(fields)

	if h.logger.IsLevelEnabled(logger.DebugLevel) {
		dump, _ := httputil.DumpRequest(req, false)
		h.logger.Debug(string(dump))
	}

	resp := &http.Response{
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
	}

	if h.md.proxyAgent != "" {
		resp.Header.Add("Proxy-Agent", h.md.proxyAgent)
	}

	/*
		if !Can("tcp", host, h.options.Whitelist, h.options.Blacklist) {
			log.Logf("[http] %s - %s : Unauthorized to tcp connect to %s",
				conn.RemoteAddr(), conn.LocalAddr(), host)
			resp.StatusCode = http.StatusForbidden

			if Debug {
				dump, _ := httputil.DumpResponse(resp, false)
				log.Logf("[http] %s <- %s\n%s", conn.RemoteAddr(), conn.LocalAddr(), string(dump))
			}

			resp.Write(conn)
			return
		}
	*/

	/*
		if h.options.Bypass.Contains(host) {
			resp.StatusCode = http.StatusForbidden

			log.Logf("[http] %s - %s bypass %s",
				conn.RemoteAddr(), conn.LocalAddr(), host)
			if Debug {
				dump, _ := httputil.DumpResponse(resp, false)
				log.Logf("[http] %s <- %s\n%s", conn.RemoteAddr(), conn.LocalAddr(), string(dump))
			}

			resp.Write(conn)
			return
		}
	*/

	if !h.authenticate(conn, req, resp) {
		return
	}

	if req.Method == "PRI" ||
		(req.Method != http.MethodConnect && req.URL.Scheme != "http") {
		resp.StatusCode = http.StatusBadRequest
		resp.Write(conn)

		if h.logger.IsLevelEnabled(logger.DebugLevel) {
			dump, _ := httputil.DumpResponse(resp, false)
			h.logger.Debug(string(dump))
		}

		return
	}

	req.Header.Del("Proxy-Authorization")

	cc, err := h.dial(ctx, host)
	if err != nil {
		resp.StatusCode = http.StatusServiceUnavailable
		resp.Write(conn)

		if h.logger.IsLevelEnabled(logger.DebugLevel) {
			dump, _ := httputil.DumpResponse(resp, false)
			h.logger.Debug(string(dump))
		}
		return
	}
	defer cc.Close()

	if req.Method == http.MethodConnect {
		resp.StatusCode = http.StatusOK
		resp.Status = "200 Connection established"

		if h.logger.IsLevelEnabled(logger.DebugLevel) {
			dump, _ := httputil.DumpResponse(resp, false)
			h.logger.Debug(string(dump))
		}
		if err = resp.Write(conn); err != nil {
			h.logger.Warn(err)
			return
		}
	} else {
		req.Header.Del("Proxy-Connection")
		if err = req.Write(cc); err != nil {
			h.logger.Warn(err)
			return
		}
	}

	handler.Transport(conn, cc)
}

func (h *Handler) dial(ctx context.Context, addr string) (conn net.Conn, err error) {
	count := h.md.retryCount + 1
	if count <= 0 {
		count = 1
	}

	for i := 0; i < count; i++ {
		route := h.chain.GetRoute()

		/*
			buf := bytes.Buffer{}
			fmt.Fprintf(&buf, "%s -> %s -> ",
				conn.RemoteAddr(), h.options.Node.String())
			for _, nd := range route.route {
				fmt.Fprintf(&buf, "%d@%s -> ", nd.ID, nd.String())
			}
			fmt.Fprintf(&buf, "%s", host)
			log.Log("[route]", buf.String())
		*/

		/*
			// forward http request
			lastNode := route.LastNode()
			if req.Method != http.MethodConnect && lastNode.Protocol == "http" {
				err = h.forwardRequest(conn, req, route)
				if err == nil {
					return
				}
				log.Logf("[http] %s -> %s : %s", conn.RemoteAddr(), conn.LocalAddr(), err)
				continue
			}
		*/

		conn, err = route.Dial(ctx, "tcp", addr)
		if err == nil {
			break
		}
		h.logger.Errorf("route(retry=%d): %s", i, err)
	}

	return
}

func (h *Handler) decodeServerName(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	if len(b) < 4 {
		return "", errors.New("invalid name")
	}
	v, err := base64.RawURLEncoding.DecodeString(string(b[4:]))
	if err != nil {
		return "", err
	}
	if crc32.ChecksumIEEE(v) != binary.BigEndian.Uint32(b[:4]) {
		return "", errors.New("invalid name")
	}
	return string(v), nil
}

func (h *Handler) basicProxyAuth(proxyAuth string) (username, password string, ok bool) {
	if proxyAuth == "" {
		return
	}

	if !strings.HasPrefix(proxyAuth, "Basic ") {
		return
	}
	c, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(proxyAuth, "Basic "))
	if err != nil {
		return
	}
	cs := string(c)
	s := strings.IndexByte(cs, ':')
	if s < 0 {
		return
	}

	return cs[:s], cs[s+1:], true
}

func (h *Handler) authenticate(conn net.Conn, req *http.Request, resp *http.Response) (ok bool) {
	u, p, _ := h.basicProxyAuth(req.Header.Get("Proxy-Authorization"))
	if h.md.authenticator == nil || h.md.authenticator.Authenticate(u, p) {
		return true
	}

	pr := h.md.probeResist
	// probing resistance is enabled, and knocking host is mismatch.
	if pr != nil && (pr.Knock == "" || !strings.EqualFold(req.URL.Hostname(), pr.Knock)) {
		resp.StatusCode = http.StatusServiceUnavailable // default status code

		switch pr.Type {
		case "code":
			resp.StatusCode, _ = strconv.Atoi(pr.Value)
		case "web":
			url := pr.Value
			if !strings.HasPrefix(url, "http") {
				url = "http://" + url
			}
			if r, err := http.Get(url); err == nil {
				resp = r
				defer r.Body.Close()
			}
		case "host":
			cc, err := net.Dial("tcp", pr.Value)
			if err == nil {
				defer cc.Close()

				req.Write(cc)
				handler.Transport(conn, cc)
				return
			}
		case "file":
			f, _ := os.Open(pr.Value)
			if f != nil {
				resp.StatusCode = http.StatusOK
				if finfo, _ := f.Stat(); finfo != nil {
					resp.ContentLength = finfo.Size()
				}
				resp.Header.Set("Content-Type", "text/html")
				resp.Body = f
			}
		}
	}

	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusProxyAuthRequired
		resp.Header.Add("Proxy-Authenticate", "Basic realm=\"gost\"")
		if strings.ToLower(req.Header.Get("Proxy-Connection")) == "keep-alive" {
			// XXX libcurl will keep sending auth request in same conn
			// which we don't supported yet.
			resp.Header.Add("Connection", "close")
			resp.Header.Add("Proxy-Connection", "close")
		}

		h.logger.Info("proxy authentication required")
	} else {
		resp.Header = http.Header{}
		resp.Header.Set("Server", "nginx/1.20.1")
		resp.Header.Set("Date", time.Now().Format(http.TimeFormat))
		if resp.StatusCode == http.StatusOK {
			resp.Header.Set("Connection", "keep-alive")
		}
	}

	if h.logger.IsLevelEnabled(logger.DebugLevel) {
		dump, _ := httputil.DumpResponse(resp, false)
		h.logger.Debug(string(dump))
	}

	resp.Write(conn)
	return
}
