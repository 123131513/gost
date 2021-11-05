package http

import "github.com/go-gost/gost/pkg/auth"

const (
	proxyAgentKey  = "proxyAgent"
	authsKey       = "auths"
	probeResistKey = "probeResist"
	knockKey       = "knock"
	retryCount     = "retry"
)

type metadata struct {
	authenticator auth.Authenticator
	proxyAgent    string
	retryCount    int
	probeResist   *probeResist
}

type probeResist struct {
	Type  string
	Value string
	Knock string
}
