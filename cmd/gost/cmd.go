package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-gost/gost/pkg/config"
	"github.com/go-gost/gost/pkg/registry"
)

var (
	ErrInvalidCmd  = errors.New("invalid cmd")
	ErrInvalidNode = errors.New("invalid node")
)

type stringList []string

func (l *stringList) String() string {
	return fmt.Sprintf("%s", *l)
}
func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

func buildConfigFromCmd(services, nodes stringList) (*config.Config, error) {
	cfg := &config.Config{}

	var chain *config.ChainConfig
	if len(nodes) > 0 {
		chain = &config.ChainConfig{
			Name: "chain-0",
		}
		cfg.Chains = append(cfg.Chains, chain)
	}

	for i, node := range nodes {
		url, err := normCmd(node)
		if err != nil {
			return nil, err
		}

		nodeConfig, err := buildNodeConfig(url)
		if err != nil {
			return nil, err
		}
		nodeConfig.Name = "node-0"

		chain.Hops = append(chain.Hops, &config.HopConfig{
			Name:  fmt.Sprintf("hop-%d", i),
			Nodes: []*config.NodeConfig{nodeConfig},
		})
	}

	for i, svc := range services {
		url, err := normCmd(svc)
		if err != nil {
			return nil, err
		}

		service, err := buildServiceConfig(url)
		if err != nil {
			return nil, err
		}
		service.Name = fmt.Sprintf("service-%d", i)
		if chain != nil {
			service.Handler.Chain = chain.Name
		}
		cfg.Services = append(cfg.Services, service)
	}

	return cfg, nil
}

func buildServiceConfig(url *url.URL) (*config.ServiceConfig, error) {
	var handler, listener string
	schemes := strings.Split(url.Scheme, "+")
	if len(schemes) == 1 {
		handler = schemes[0]
		listener = schemes[0]
	}
	if len(schemes) == 2 {
		handler = schemes[0]
		listener = schemes[1]
	}

	svc := &config.ServiceConfig{
		Addr: url.Host,
	}

	if h := registry.GetHandler(handler); h == nil {
		handler = "auto"
	}
	if ln := registry.GetListener(listener); ln == nil {
		listener = "tcp"
		if handler == "ssu" {
			listener = "udp"
		}
	}

	if remotes := strings.Trim(url.EscapedPath(), "/"); remotes != "" {
		svc.Forwarder = &config.ForwarderConfig{
			Targets: strings.Split(remotes, ","),
		}
		if handler != "relay" {
			if listener == "tcp" || listener == "udp" ||
				listener == "rtcp" || listener == "rudp" ||
				listener == "tun" || listener == "tap" {
				handler = listener
			} else {
				handler = "tcp"
			}
		}
	}

	var auths []*config.AuthConfig
	if url.User != nil {
		auth := &config.AuthConfig{
			Username: url.User.Username(),
		}
		auth.Password, _ = url.User.Password()
		auths = append(auths, auth)
	}

	md := make(map[string]interface{})
	for k, v := range url.Query() {
		if len(v) > 0 {
			md[k] = v[0]
		}
	}
	if sauth := md["auth"]; sauth != nil {
		if sa, _ := sauth.(string); sa != "" {
			au, err := parseAuthFromCmd(sa)
			if err != nil {
				return nil, err
			}
			auths = append(auths, au)
		}
	}
	delete(md, "auth")

	var tlsConfig *config.TLSConfig
	if certs := md["cert"]; certs != nil {
		cert, _ := certs.(string)
		key, _ := md["key"].(string)
		ca, _ := md["ca"].(string)
		tlsConfig = &config.TLSConfig{
			Cert: cert,
			Key:  key,
			CA:   ca,
		}
	}
	delete(md, "cert")
	delete(md, "key")
	delete(md, "ca")

	svc.Handler = &config.HandlerConfig{
		Type:     handler,
		Auths:    auths,
		Metadata: md,
	}
	svc.Listener = &config.ListenerConfig{
		Type:     listener,
		TLS:      tlsConfig,
		Metadata: md,
	}

	return svc, nil
}

func buildNodeConfig(url *url.URL) (*config.NodeConfig, error) {
	var connector, dialer string
	schemes := strings.Split(url.Scheme, "+")
	if len(schemes) == 1 {
		connector = schemes[0]
		dialer = schemes[0]
	}
	if len(schemes) == 2 {
		connector = schemes[0]
		dialer = schemes[1]
	}

	node := &config.NodeConfig{
		Addr: url.Host,
	}

	if c := registry.GetConnector(connector); c == nil {
		connector = "http"
	}
	if d := registry.GetDialer(dialer); d == nil {
		dialer = "tcp"
		if connector == "ssu" {
			dialer = "udp"
		}
	}

	var auth *config.AuthConfig
	if url.User != nil {
		auth = &config.AuthConfig{
			Username: url.User.Username(),
		}
		auth.Password, _ = url.User.Password()
	}

	md := make(map[string]interface{})
	for k, v := range url.Query() {
		if len(v) > 0 {
			md[k] = v[0]
		}
	}
	md["serverName"] = url.Host

	if sauth := md["auth"]; sauth != nil && auth == nil {
		if sa, _ := sauth.(string); sa != "" {
			au, err := parseAuthFromCmd(sa)
			if err != nil {
				return nil, err
			}
			auth = au
		}
	}
	delete(md, "auth")

	var tlsConfig *config.TLSConfig
	if certs := md["cert"]; certs != nil {
		cert, _ := certs.(string)
		key, _ := md["key"].(string)
		ca, _ := md["ca"].(string)
		secure, _ := md["secure"].(bool)
		serverName, _ := md["serverName"].(string)
		tlsConfig = &config.TLSConfig{
			Cert:       cert,
			Key:        key,
			CA:         ca,
			Secure:     secure,
			ServerName: serverName,
		}
	}
	delete(md, "cert")
	delete(md, "key")
	delete(md, "ca")
	delete(md, "secure")
	delete(md, "serverName")

	node.Connector = &config.ConnectorConfig{
		Type:     connector,
		Auth:     auth,
		Metadata: md,
	}
	node.Dialer = &config.DialerConfig{
		Type:     dialer,
		TLS:      tlsConfig,
		Metadata: md,
	}

	return node, nil
}

func normCmd(s string) (*url.URL, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrInvalidCmd
	}

	if !strings.Contains(s, "://") {
		s = "auto://" + s
	}

	url, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	if url.Scheme == "https" {
		url.Scheme = "http+tls"
	}

	return url, nil
}

func parseAuthFromCmd(sa string) (*config.AuthConfig, error) {
	v, err := base64.StdEncoding.DecodeString(sa)
	if err != nil {
		return nil, err
	}
	cs := string(v)
	n := strings.IndexByte(cs, ':')
	if n < 0 {
		return &config.AuthConfig{
			Username: cs,
		}, nil
	}

	return &config.AuthConfig{
		Username: cs[:n],
		Password: cs[n+1:],
	}, nil
}
