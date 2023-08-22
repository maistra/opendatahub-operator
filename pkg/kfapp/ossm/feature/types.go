package feature

import (
	"strings"
)

type oAuth struct {
	AuthzEndpoint,
	TokenEndpoint,
	Route,
	Port,
	ClientSecret,
	Hmac string
}

func ReplaceChar(s string, oldChar, newChar string) string {
	return strings.ReplaceAll(s, oldChar, newChar)
}
