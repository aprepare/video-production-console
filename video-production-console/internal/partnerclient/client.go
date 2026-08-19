package partnerclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	pinnedGatewayServerName = "23.138.12.112"
	maxJSONResponseBytes    = 1 << 20
)

var (
	ErrInvalidPinnedCA        = errors.New("invalid pinned gateway CA")
	ErrGatewayUnavailable     = errors.New("partner gateway unavailable")
	ErrInvalidGatewayResponse = errors.New("invalid partner gateway response")
	ErrAuthorizationFailed    = errors.New("partner authorization failed")
	ErrDeviceMismatch         = errors.New("partner device mismatch")
	ErrRateLimited            = errors.New("partner gateway rate limited")
	ErrModelNotAllowed        = errors.New("partner model not allowed")
	ErrUpstreamUnavailable    = errors.New("partner upstream unavailable")
	ErrGatewayRejected        = errors.New("partner gateway rejected request")
)

type ClientOptions struct {
	BaseURL     string
	CAPEM       []byte
	DialContext func(context.Context, string, string) (net.Conn, error)
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(options ClientOptions) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimSpace(options.BaseURL))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil {
		return nil, ErrInvalidGatewayResponse
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(options.CAPEM) {
		return nil, ErrInvalidPinnedCA
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
		ServerName: pinnedGatewayServerName,
	}
	transport.Proxy = http.ProxyFromEnvironment
	if options.DialContext != nil {
		transport.DialContext = options.DialContext
	}

	return &Client{
		baseURL: strings.TrimRight(baseURL.String(), "/"),
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) Activate(ctx context.Context, request ActivateRequest) (AuthResponse, error) {
	var response AuthResponse
	err := c.doJSON(ctx, http.MethodPost, "/auth/activate", request, "", &response)
	return response, err
}

func (c *Client) Verify(ctx context.Context, request VerifyRequest) (AuthResponse, error) {
	var response AuthResponse
	err := c.doJSON(ctx, http.MethodPost, "/auth/verify", request, "", &response)
	return response, err
}

func (c *Client) Models(ctx context.Context, session string) (Capabilities, error) {
	var response Capabilities
	err := c.doJSON(ctx, http.MethodGet, "/v1/models", nil, session, &response)
	return response, err
}

func (c *Client) RoundTripper(session func() string) http.RoundTripper {
	base := c.httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if session == nil {
		session = func() string { return "" }
	}
	return roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		cloned := request.Clone(request.Context())
		cloned.Header = request.Header.Clone()
		cloned.Header.Set("Authorization", "Bearer "+session())
		return base.RoundTrip(cloned)
	})
}

func (c *Client) doJSON(
	ctx context.Context,
	method string,
	path string,
	payload any,
	session string,
	destination any,
) error {
	endpoint, err := c.endpoint(path)
	if err != nil {
		return err
	}

	var body io.Reader
	if payload != nil {
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return ErrInvalidGatewayResponse
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return ErrInvalidGatewayResponse
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		request.Header.Set("Authorization", "Bearer "+session)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrGatewayUnavailable
	}
	defer response.Body.Close()

	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return ErrInvalidGatewayResponse
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxJSONResponseBytes+1))
	if err != nil {
		return ErrInvalidGatewayResponse
	}
	if len(raw) > maxJSONResponseBytes {
		return ErrInvalidGatewayResponse
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return mapGatewayError(raw)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return ErrInvalidGatewayResponse
	}
	return nil
}

func (c *Client) endpoint(path string) (string, error) {
	baseURL, err := url.Parse(c.baseURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil {
		return "", ErrInvalidGatewayResponse
	}
	baseURL.Path = path
	baseURL.RawPath = ""
	baseURL.RawQuery = ""
	baseURL.Fragment = ""
	return baseURL.String(), nil
}

func mapGatewayError(raw []byte) error {
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return ErrInvalidGatewayResponse
	}
	switch response.Error.Code {
	case "authorization_failed":
		return ErrAuthorizationFailed
	case "device_mismatch":
		return ErrDeviceMismatch
	case "rate_limited":
		return ErrRateLimited
	case "model_not_allowed":
		return ErrModelNotAllowed
	case "upstream_unavailable":
		return ErrUpstreamUnavailable
	case "not_found", "method_not_allowed":
		return ErrGatewayRejected
	default:
		return ErrGatewayRejected
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
