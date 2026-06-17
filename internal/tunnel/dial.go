package tunnel

import (
	"context"
	"crypto/tls"
	"net/http"

	"github.com/hashicorp/yamux"
	"nhooyr.io/websocket"
)

func Dial(ctx context.Context, endpoint string, tlsConfig *tls.Config) (*yamux.Session, error) {
	return DialWithHeaders(ctx, endpoint, tlsConfig, nil)
}

func DialWithHeaders(ctx context.Context, endpoint string, tlsConfig *tls.Config, headers http.Header) (*yamux.Session, error) {
	ws, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
			Timeout:   0,
		},
		HTTPHeader: headers,
	})
	if err != nil {
		return nil, err
	}

	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	mux, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	return mux, nil
}
