package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

// streamIdleTimeout cancels an upstream stream that stops producing data.
const streamIdleTimeout = 300 * time.Second

// Stream forwards an SSE request upstream, flushing every received chunk to
// the client. flush should be the HTTP framework's response flusher.
func (p *Proxy) Stream(ctx context.Context, w http.ResponseWriter, flush func(), req Request) (Result, error) {
	return p.forward(ctx, w, req, true, flush)
}

func (p *Proxy) copyStream(ctx, callCtx context.Context, cancel context.CancelFunc, w http.ResponseWriter, captured *captureWriter, flush func(), resp *http.Response, protocol string, start time.Time, result Result) (Result, error) {
	if flush == nil {
		flush = func() {}
	}
	parser := NewStreamParser(protocol)
	idle := time.AfterFunc(streamIdleTimeout, cancel)
	defer idle.Stop()

	firstByte := true
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if firstByte {
				result.FirstByteMS = int(time.Since(start).Milliseconds())
				firstByte = false
			}
			idle.Reset(streamIdleTimeout)
			parser.Feed(buf[:n])
			if _, writeErr := captured.Write(buf[:n]); writeErr != nil {
				result.ResponseBody = captured.bytes()
				result.Usage = parser.Usage()
				result.ClientDisconnected = true
				return result, ErrClientDisconnected
			}
			flush()
		}

		if readErr != nil {
			result.ResponseBody = captured.bytes()
			result.Usage = parser.Usage()
			if errors.Is(readErr, io.EOF) {
				return result, nil
			}
			switch {
			case ctx.Err() != nil:
				result.ClientDisconnected = true
				return result, ErrClientDisconnected
			case callCtx.Err() != nil:
				return result, ErrUpstreamTimeout
			default:
				return result, ErrUpstreamUnavailable
			}
		}
	}
}
