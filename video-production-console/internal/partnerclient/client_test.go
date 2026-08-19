package partnerclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClientTrustsOnlyPinnedCA(t *testing.T) {
	goodServer, caPEM := newTLSServerForIP(t, "23.138.12.112")
	badServer, _ := newTLSServerForIP(t, "23.138.12.112")
	client, err := NewClient(ClientOptions{
		BaseURL:     goodServer.URL,
		CAPEM:       caPEM,
		DialContext: dialTestServer(goodServer),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Models(context.Background(), "session"); err != nil {
		t.Fatal(err)
	}
	client.baseURL = badServer.URL
	client.httpClient.Transport = transportWithPinnedCA(caPEM, dialTestServer(badServer))
	if _, err := client.Models(context.Background(), "session"); err == nil {
		t.Fatal("untrusted CA accepted")
	}
}

func TestNewClientRejectsWrongIPSAN(t *testing.T) {
	server, caPEM := newTLSServerForIP(t, "192.0.2.10")
	client, err := NewClient(ClientOptions{
		BaseURL:     server.URL,
		CAPEM:       caPEM,
		DialContext: dialTestServer(server),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Models(context.Background(), "session"); err == nil {
		t.Fatal("certificate for wrong IP SAN accepted")
	}
}

func TestNewClientRejectsExpiredCertificate(t *testing.T) {
	now := time.Now()
	server, caPEM := newTLSServerWithCertificate(
		t,
		"23.138.12.112",
		now.Add(-2*time.Hour),
		now.Add(-time.Hour),
		modelsHandler(),
	)
	client, err := NewClient(ClientOptions{
		BaseURL:     server.URL,
		CAPEM:       caPEM,
		DialContext: dialTestServer(server),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Models(context.Background(), "session"); err == nil {
		t.Fatal("expired certificate accepted")
	}
}

func TestNewClientRejectsInvalidPinnedCA(t *testing.T) {
	if _, err := NewClient(ClientOptions{
		BaseURL: "https://23.138.12.112:2443",
		CAPEM:   []byte("not a certificate"),
	}); !errors.Is(err, ErrInvalidPinnedCA) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewClientRequiresJSONAndCapsResponses(t *testing.T) {
	tests := []struct {
		name    string
		handler http.Handler
	}{
		{
			name: "content type",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(w, `{"features":[]}`)
			}),
		},
		{
			name: "response size",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"features":["`+strings.Repeat("x", maxJSONResponseBytes)+`"]}`)
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, caPEM := newTLSServerWithCertificate(
				t,
				"23.138.12.112",
				time.Now().Add(-time.Hour),
				time.Now().Add(time.Hour),
				test.handler,
			)
			client, err := NewClient(ClientOptions{
				BaseURL:     server.URL,
				CAPEM:       caPEM,
				DialContext: dialTestServer(server),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Models(context.Background(), "session"); !errors.Is(err, ErrInvalidGatewayResponse) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestNewClientMapsStableErrorsWithoutLeakingBody(t *testing.T) {
	const secretBody = "PRIVATE-UPSTREAM-DETAIL"
	server, caPEM := newTLSServerWithCertificate(
		t,
		"23.138.12.112",
		time.Now().Add(-time.Hour),
		time.Now().Add(time.Hour),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"authorization_failed","message":"`+secretBody+`"}}`)
		}),
	)
	client, err := NewClient(ClientOptions{
		BaseURL:     server.URL,
		CAPEM:       caPEM,
		DialContext: dialTestServer(server),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Models(context.Background(), "expired-session")
	if !errors.Is(err, ErrAuthorizationFailed) {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(err.Error(), secretBody) {
		t.Fatalf("raw gateway body leaked in error: %v", err)
	}
}

func TestNewClientRoundTripperAlwaysUsesCurrentSession(t *testing.T) {
	recorder := &recordingTransport{}
	client := &Client{httpClient: &http.Client{Transport: recorder}}
	currentSession := "session-one"
	transport := client.RoundTripper(func() string { return currentSession })

	request, err := http.NewRequest(http.MethodGet, "https://gateway.invalid/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer caller-controlled")
	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if got := recorder.authorization; got != "Bearer session-one" {
		t.Fatalf("authorization=%q", got)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer caller-controlled" {
		t.Fatalf("original request mutated: authorization=%q", got)
	}

	currentSession = "session-two"
	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if got := recorder.authorization; got != "Bearer session-two" {
		t.Fatalf("authorization=%q", got)
	}
}

type recordingTransport struct {
	authorization string
}

func (t *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.authorization = request.Header.Get("Authorization")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    request,
	}, nil
}

func modelsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"features":["text"],"text_models":["gpt-5.6-sol"]}`)
	})
}

func newTLSServerForIP(t *testing.T, ip string) (*httptest.Server, []byte) {
	t.Helper()
	return newTLSServerWithCertificate(
		t,
		ip,
		time.Now().Add(-time.Hour),
		time.Now().Add(time.Hour),
		modelsHandler(),
	)
}

func newTLSServerWithCertificate(
	t *testing.T,
	ip string,
	notBefore time.Time,
	notAfter time.Time,
	handler http.Handler,
) (*httptest.Server, []byte) {
	t.Helper()
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "partner-client-test-ca"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leafPublic, leafPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: ip},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP(ip)},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCertificate, leafPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	serverCertificate := tls.Certificate{
		Certificate: [][]byte{leafDER, caDER},
		PrivateKey:  leafPrivate,
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCertificate},
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

func dialTestServer(server *httptest.Server) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
}

func transportWithPinnedCA(
	caPEM []byte,
	dialContext func(context.Context, string, string) (net.Conn, error),
) *http.Transport {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
		ServerName: pinnedGatewayServerName,
	}
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = dialContext
	return transport
}
