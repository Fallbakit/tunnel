package tunnel

import (
	"context"
	"io"
	"time"
)

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func newContextReader(ctx context.Context, r io.Reader) io.Reader {
	return &contextReader{ctx: ctx, r: r}
}

func (r *contextReader) Read(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}

	ch := make(chan result, 1)
	go func() {
		n, err := r.r.Read(p)
		ch <- result{n: n, err: err}
	}()

	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case res := <-ch:
		return res.n, res.err
	case <-time.After(30 * time.Second):
		return 0, context.DeadlineExceeded
	}
}
