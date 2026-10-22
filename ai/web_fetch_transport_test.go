package ai

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		address string
		private bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.1.2", true},
		{"192.168.1.2", true},
		{"169.254.1.2", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"fc00::1", true},
		{"fe80::1", true},
		{"8.8.8.8", false},
		{"2001:4860:4860::8888", false},
		{"64:ff9b::a00:1", true},
		{"64:ff9b:1::a00:1", true},
		{"2002:a00:1::1", true},
		{"2002:7f00:1::1", true},
		{"2002:808:808::1", false},
		{"198.18.0.1", true},
		{"::ffff:100.64.0.1", true},
	}
	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			addr, err := netip.ParseAddr(tt.address)
			require.NoError(t, err)
			assert.Equal(t, tt.private, isPrivateIP(addr))
		})
	}
}

func TestWebFetchGuardedDialRejectsLookupError(t *testing.T) {
	dialCalled := false
	transport := newWebFetchTransportWithDial(false,
		func(context.Context, string, string) ([]netip.Addr, error) { return nil, errors.New("lookup failed") },
		func(context.Context, string, string) (net.Conn, error) {
			dialCalled = true
			return nil, nil
		},
	)
	_, err := transport.DialContext(context.Background(), "tcp", "example.test:80")
	require.Error(t, err)
	assert.False(t, dialCalled)
}

func TestWebFetchGuardedDialUsesValidatedAddresses(t *testing.T) {
	lookups := 0
	dialed := ""
	transport := newWebFetchTransportWithDial(false,
		func(context.Context, string, string) ([]netip.Addr, error) {
			lookups++
			if lookups == 1 {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		func(_ context.Context, network, address string) (net.Conn, error) {
			dialed = network + " " + address
			return nil, nil
		},
	)
	conn, err := transport.DialContext(context.Background(), "tcp", "example.test:443")
	require.NoError(t, err)
	assert.Nil(t, conn)
	assert.Equal(t, 1, lookups)
	assert.Equal(t, "tcp 93.184.216.34:443", dialed)
}

func TestWebFetchGuardedDialRejectsMixedAddresses(t *testing.T) {
	dialCalled := false
	transport := newWebFetchTransportWithDial(false,
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			dialCalled = true
			return nil, nil
		},
	)
	_, err := transport.DialContext(context.Background(), "tcp", "example.test:80")
	require.Error(t, err)
	assert.False(t, dialCalled)
}

func TestWebFetchTransportProxyPolicy(t *testing.T) {
	guarded := newWebFetchTransport(false)
	assert.Nil(t, guarded.Proxy)
	assert.Nil(t, guarded.DialTLSContext)
	assert.NotNil(t, newWebFetchTransport(true).Proxy)
}

func TestWebFetchGuardSurvivesRequestHeaderClone(t *testing.T) {
	tool := NewWebFetchTool(WebToolConfig{Enabled: true, AllowedSchemes: []string{"http", "https"}})
	client := withRequestHeaders(tool.client, http.Header{"User-Agent": []string{"test"}})

	wrapped, ok := client.Transport.(roundTripperWithHeaders)
	require.True(t, ok)
	base, ok := wrapped.base.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, base.Proxy)
	assert.NotNil(t, base.DialContext)

	_, err := client.Get("http://127.0.0.1:9/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked private address")
}

func TestWebFetchRedirectKeepsAllowedSchemes(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/", http.StatusFound)
	}))
	defer server.Close()

	tool := NewWebFetchTool(WebToolConfig{Enabled: true, AllowedSchemes: []string{"https"}, AllowPrivateNetworks: true})
	tool.client.Transport = server.Client().Transport

	_, err := tool.client.Get(server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `redirect to unsupported URL scheme "http"`)
}
