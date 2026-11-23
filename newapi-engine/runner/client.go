package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"unicode/utf16"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

var ErrClosed = errors.New("plugin runner is closed")

// RPC contains opaque JSON members. Escaping HTML here both changes their
// wire text and can inflate a valid 1 MiB ordered body beyond the RPC limits.
func marshalFrame(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), nil
}

type Client struct {
	Meta       jsplugin.Meta
	Descriptor Descriptor
	cmd        *exec.Cmd
	input      io.WriteCloser
	output     *bufio.Scanner
	slot       chan struct{}
	closed     chan struct{}
	closeOnce  sync.Once
	waited     chan struct{}
	nextID     uint64 // guarded by slot
}

// Start starts the host-owned runner executable, without inheriting environment
// variables or accepting a plugin-supplied command. The caller owns Close.
func Start(ctx context.Context, executable string, args []string, source string) (*Client, error) {
	if len(source) == 0 || len(source) > MaxSourceBytes {
		return nil, errors.New("plugin source exceeds limit or is empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, args...)
	cmd.Env = []string{}
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), MaxFrameBytes)
	client := &Client{cmd: cmd, input: input, output: scanner, slot: make(chan struct{}, 1), closed: make(chan struct{}), waited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(client.waited); client.close() }()
	res, err := client.exchange(ctx, request{Source: source})
	if err != nil {
		client.Close()
		return nil, err
	}
	if res.Meta == nil {
		client.Close()
		return nil, errors.New("plugin runner did not return metadata")
	}
	client.Meta = *res.Meta
	if res.Descriptor == nil {
		client.Close()
		return nil, errors.New("plugin runner did not return capability descriptor")
	}
	client.Descriptor = *res.Descriptor
	return client, nil
}

func (c *Client) ValidateConfig(ctx context.Context, config json.RawMessage, instance bool) (json.RawMessage, error) {
	operation := "channel_config"
	if instance {
		operation = "instance_config"
	}
	res, err := c.exchange(ctx, request{Operation: operation, Args: []json.RawMessage{config}})
	return bytes.Clone(res.Result), err
}

func (c *Client) IsClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *Client) Call(ctx context.Context, hook string, path []string, args ...any) (json.RawMessage, error) {
	return c.CallWithFetcher(ctx, nil, hook, path, args...)
}

func (c *Client) CallWithFetcher(ctx context.Context, fetcher jsplugin.Fetcher, hook string, path []string, args ...any) (json.RawMessage, error) {
	if len(path) > 2 || len(args) > 8 {
		return nil, errors.New("plugin call exceeds contract bounds")
	}
	req := request{Hook: hook, Path: path, Args: make([]json.RawMessage, len(args)), AllowFetch: fetcher != nil}
	for i, value := range args {
		encoded, err := marshalFrame(value)
		if err != nil {
			return nil, err
		}
		if len(encoded) > MaxResultBytes {
			return nil, errors.New("plugin argument exceeds limit")
		}
		req.Args[i] = encoded
	}
	res, err := c.exchangeWithFetcher(ctx, req, fetcher)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(res.Result), nil
}

func (c *Client) exchange(parent context.Context, req request) (response, error) {
	return c.exchangeWithFetcher(parent, req, nil)
}

func (c *Client) exchangeWithFetcher(parent context.Context, req request, fetcher jsplugin.Fetcher) (response, error) {
	return c.exchangeWithMedia(parent, req, fetcher, nil)
}

func (c *Client) exchangeWithMedia(parent context.Context, req request, fetcher jsplugin.Fetcher, media jsplugin.MediaSource) (response, error) {
	ctx, cancel := context.WithTimeout(parent, MaxCallTime)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return response{}, err
	}
	select {
	case <-ctx.Done():
		return response{}, ctx.Err()
	case <-c.closed:
		return response{}, ErrClosed
	case c.slot <- struct{}{}:
	}
	defer func() { <-c.slot }()
	select {
	case <-c.closed:
		return response{}, ErrClosed
	default:
	}
	c.nextID++
	req.ID = c.nextID
	frame, err := marshalFrame(req)
	if err != nil {
		return response{}, err
	}
	if len(frame) >= MaxFrameBytes {
		return response{}, errors.New("plugin frame exceeds limit")
	}
	type outcome struct {
		result response
		err    error
	}
	done := make(chan outcome, 1)
	// At most one I/O goroutine exists per client. Cancellation kills the child
	// and closes its pipes; waiting here guarantees it does not outlive this call.
	go func() {
		if _, err := c.input.Write(append(frame, '\n')); err != nil {
			done <- outcome{err: err}
			return
		}
		textUnits := 0
		for fetches, mediaReads := 0, 0; ; {
			if !c.output.Scan() {
				err := c.output.Err()
				if err == nil {
					err = io.ErrUnexpectedEOF
				}
				done <- outcome{err: err}
				return
			}
			var res response
			err := json.Unmarshal(c.output.Bytes(), &res)
			if err == nil && res.ID != req.ID {
				err = errors.New("plugin frame ID mismatch")
			}
			if err == nil && res.Media != nil {
				mediaReads++
				if media == nil || mediaReads > maxMediaCallbacks || res.Fetch != nil || res.Result != nil || res.Error != "" {
					done <- outcome{err: errors.New("plugin media callback exceeds contract")}
					return
				}
				value, readErr := readMedia(ctx, media, *res.Media)
				if readErr == nil && value.Text != nil {
					textUnits += len(utf16.Encode([]rune(value.Text.Text)))
					if textUnits > 512<<10 {
						readErr = errors.New("media text budget exceeded")
					}
				}
				answer := request{ID: req.ID, Operation: "media_result", MediaResult: value}
				if readErr != nil {
					answer.MediaResult, answer.MediaError = nil, "host media unavailable"
				}
				if e := json.NewEncoder(c.input).Encode(answer); e != nil {
					done <- outcome{err: e}
					return
				}
				continue
			}
			if err == nil && res.Fetch != nil {
				fetches++
				if fetcher == nil || fetches > 4 {
					done <- outcome{err: errors.New("plugin fetch callback exceeds contract")}
					return
				}
				fetched, fetchErr := fetchWithMedia(ctx, fetcher, media, *res.Fetch)
				answer := request{ID: req.ID, Operation: "fetch_result", FetchResult: &fetched}
				if fetchErr != nil {
					answer.FetchResult = nil
					answer.FetchError = "host fetch failed"
				}
				if len(fetched.Body) > jsplugin.MaxFetchBodyBytes {
					answer.FetchResult = nil
					answer.FetchError = "host fetch response exceeds limit"
				}
				if e := json.NewEncoder(c.input).Encode(answer); e != nil {
					done <- outcome{err: e}
					return
				}
				continue
			}
			done <- outcome{res, err}
			return
		}
	}()
	select {
	case <-ctx.Done():
		c.close()
		<-done
		return response{}, ctx.Err()
	case result := <-done:
		if result.err != nil {
			c.close()
			return response{}, fmt.Errorf("plugin process failed: %w", result.err)
		}
		if result.result.Error != "" {
			return response{}, errors.New(result.result.Error)
		}
		return result.result, nil
	}
}

func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.input.Close()
		_ = c.cmd.Process.Kill()
	})
}

func (c *Client) Close() { c.close(); <-c.waited }
