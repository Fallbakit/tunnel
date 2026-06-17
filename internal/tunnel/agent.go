package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/yamux"
)

type Agent struct {
	session     *yamux.Session
	localBase   *url.URL
	localAPIKey string
	client      *http.Client
}

func NewAgent(session *yamux.Session, ollamaBase string) (*Agent, error) {
	return NewAgentWithLocalAPIKey(session, ollamaBase, "")
}

func NewAgentWithLocalAPIKey(session *yamux.Session, localBase string, localAPIKey string) (*Agent, error) {
	if session == nil {
		return nil, errors.New("nil tunnel session")
	}

	base, err := url.Parse(localBase)
	if err != nil {
		return nil, err
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, errors.New("local runtime base URL must include scheme and host")
	}

	return &Agent{
		session:     session,
		localBase:   base,
		localAPIKey: strings.TrimSpace(localAPIKey),
		client:      http.DefaultClient,
	}, nil
}

func (a *Agent) Serve(ctx context.Context) error {
	for {
		stream, err := a.session.AcceptStream()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return err
			}
		}

		go func(conn net.Conn) {
			defer conn.Close()
			if err := a.handleStream(ctx, conn); err != nil {
				_ = writeBadGateway(conn, err)
			}
		}(stream)
	}
}

func (a *Agent) handleStream(ctx context.Context, conn net.Conn) error {
	req, err := http.ReadRequest(bufio.NewReader(newContextReader(ctx, conn)))
	if err != nil {
		return fmt.Errorf("read request from tunnel: %w", err)
	}
	defer req.Body.Close()

	outboundURL := *a.localBase
	outboundURL.Path = joinPath(a.localBase.Path, req.URL.Path)
	outboundURL.RawQuery = req.URL.RawQuery

	outbound, err := http.NewRequestWithContext(ctx, req.Method, outboundURL.String(), req.Body)
	if err != nil {
		return err
	}
	outbound.Header = req.Header.Clone()
	if a.localAPIKey != "" {
		outbound.Header.Set("Authorization", "Bearer "+a.localAPIKey)
	}

	resp, err := a.client.Do(outbound)
	if err != nil {
		return fmt.Errorf("local runtime request failed: %w", err)
	}
	defer resp.Body.Close()

	return resp.Write(conn)
}

func joinPath(base, path string) string {
	base = strings.TrimRight(base, "/")
	path = "/" + strings.TrimLeft(path, "/")
	if base == "" {
		return path
	}
	return base + path
}

func writeBadGateway(w net.Conn, cause error) error {
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Status:     "502 Bad Gateway",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Body:       http.NoBody,
		Header:     make(http.Header),
	}
	resp.Header.Set("X-Fallbakit-Tunnel-Error", sanitizeHeader(cause.Error()))
	return resp.Write(w)
}

func sanitizeHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
