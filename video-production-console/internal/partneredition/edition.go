package partneredition

import (
	"errors"
	"net/url"
	"strings"
)

var (
	ErrInvalidEdition = errors.New("invalid edition")
	ErrInvalidGateway = errors.New("invalid partner gateway")
)

type Name string

const (
	Owner   Name = "owner"
	Partner Name = "partner"
)

var builtEdition = string(Owner)
var builtGatewayURL = ""

type Config struct {
	Name       Name
	GatewayURL string
}

func Current() (Config, error) {
	name := Name(strings.TrimSpace(builtEdition))
	switch name {
	case Owner:
		return Config{Name: Owner}, nil
	case Partner:
		u, err := url.Parse(strings.TrimSpace(builtGatewayURL))
		if err != nil || u.Scheme != "https" || u.Host != "23.138.12.112:2443" || u.Path != "" {
			return Config{}, ErrInvalidGateway
		}
		return Config{Name: Partner, GatewayURL: u.String()}, nil
	default:
		return Config{}, ErrInvalidEdition
	}
}

func (c Config) IsPartner() bool {
	return c.Name == Partner
}
