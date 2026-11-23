package service

import (
	"bytes"
	"errors"
	"io"
)

// This tap observes the exact standard-protocol bytes handed to the existing
// native parser. It neither rewrites SSE nor reads ahead in a goroutine. Close
// still reaches the real body; cancellation and credential replay stay owned
// by the existing Provider exchange.
type newAPIProviderToolStream struct {
	io.ReadCloser
	tools   *newAPIProviderToolUsage
	path    string
	line    []byte
	data    bytes.Buffer
	failure error
}

func (r *newAPIProviderToolStream) observeLine() error {
	line := bytes.TrimSuffix(r.line, []byte{'\r'})
	defer func() { r.line = r.line[:0] }()
	if len(line) == 0 {
		if r.data.Len() == 0 {
			return nil
		}
		err := r.tools.observe(r.path, r.data.Bytes(), true)
		r.data.Reset()
		return err
	}
	if bytes.HasPrefix(line, []byte("data:")) {
		value := bytes.TrimPrefix(line[5:], []byte{' '})
		if len(value) > MaxRequestPluginBody-r.data.Len()-1 {
			return errors.New("Provider tool event exceeds limit")
		}
		if r.data.Len() > 0 {
			r.data.WriteByte('\n')
		}
		r.data.Write(value)
	}
	return nil
}

func (r *newAPIProviderToolStream) Read(p []byte) (int, error) {
	if r.failure != nil {
		return 0, r.failure
	}
	n, err := r.ReadCloser.Read(p)
	for _, b := range p[:n] {
		if b == '\n' {
			if observeErr := r.observeLine(); observeErr != nil {
				r.failure = observeErr
				return 0, observeErr
			}
		} else {
			if len(r.line) >= MaxRequestPluginBody {
				r.failure = errors.New("Provider tool SSE line exceeds limit")
				return 0, r.failure
			}
			r.line = append(r.line, b)
		}
	}
	if err == io.EOF {
		if len(r.line) > 0 {
			if observeErr := r.observeLine(); observeErr != nil {
				r.failure = observeErr
				return 0, observeErr
			}
		}
		if r.data.Len() > 0 {
			r.failure = r.tools.observe(r.path, r.data.Bytes(), true)
			r.data.Reset()
			if r.failure != nil {
				return 0, r.failure
			}
		}
	}
	return n, err
}
