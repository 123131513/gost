package http

import "net/url"

const (
	userAgent = "userAgent"
	username  = "username"
	password  = "password"
)

const (
	defaultUserAgent = "Chrome/78.0.3904.106"
)

type metadata struct {
	UserAgent string
	User      *url.Userinfo
}
