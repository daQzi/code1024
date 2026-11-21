package runner

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"time"
)

const MaxProcesses = 4

type lane struct {
	client   *Client
	hash     [32]byte
	lastUsed time.Time
}

// Pool owns a fixed number of process slots. Callers wait only until their
// context expires; no request goroutine or source cache is retained by Pool.
type Pool struct {
	executable string
	args       []string
	lanes      chan *lane
	stop       chan struct{}
	done       chan struct{}
	once       sync.Once
}

func NewPool(executable string, args []string) *Pool {
	p := &Pool{executable: executable, args: append([]string(nil), args...), lanes: make(chan *lane, MaxProcesses), stop: make(chan struct{}), done: make(chan struct{})}
	for range MaxProcesses {
		p.lanes <- &lane{}
	}
	go p.maintain()
	return p
}

func (p *Pool) With(ctx context.Context, source string, fn func(*Client) error) error {
	if len(source) == 0 || len(source) > MaxSourceBytes {
		return errors.New("invalid plugin source size")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var l *lane
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.stop:
		return ErrClosed
	case l = <-p.lanes:
	}
	defer func() { l.lastUsed = time.Now(); p.lanes <- l }()
	select {
	case <-p.stop:
		return ErrClosed
	default:
	}
	hash := sha256.Sum256([]byte(source))
	if l.client != nil && (l.hash != hash || l.client.IsClosed()) {
		l.client.Close()
		l.client = nil
	}
	if l.client == nil {
		client, err := Start(ctx, p.executable, p.args, source)
		if err != nil {
			return err
		}
		l.client, l.hash = client, hash
	}
	return fn(l.client)
}

func (p *Pool) maintain() {
	defer close(p.done)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			// Each in-flight operation has a hard runner deadline. Acquiring all
			// lanes drains them before releasing child resources.
			for range MaxProcesses {
				l := <-p.lanes
				if l.client != nil {
					l.client.Close()
				}
			}
			return
		case now := <-ticker.C:
			for range len(p.lanes) {
				select {
				case l := <-p.lanes:
					if l.client != nil && now.Sub(l.lastUsed) > 2*time.Minute {
						l.client.Close()
						l.client = nil
					}
					p.lanes <- l
				default:
				}
			}
		}
	}
}

func (p *Pool) Close() { p.once.Do(func() { close(p.stop) }); <-p.done }
