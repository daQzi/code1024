package officialupload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const Root = "/api/v1/video-generations/uploads"

// Do accepts only host-owned relative paths. The caller pins the upstream,
// authentication, proxy, redirects, timeouts and response-size limit.
type Do func(context.Context, string, string, map[string]string, io.Reader, int64) (json.RawMessage, error)
type Checkpoint func(context.Context) error

type Waiting struct{ Delay time.Duration }

func (e *Waiting) Error() string { return "upstream reference is being prepared" }

// Permanent reports a rejected contract, never upstream response text or tokens.
type Permanent struct{ Reason string }

func (e *Permanent) Error() string { return e.Reason }

func permanent(err error) error { return &Permanent{Reason: err.Error()} }

func decode(raw json.RawMessage, result any) error {
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	_, acknowledgement := result.(*any)
	if len(raw) > 64<<10 || json.Unmarshal(raw, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 {
		return errors.New("invalid reference-upload response")
	}
	if acknowledgement && len(envelope.Data) == 0 {
		return nil
	}
	if len(envelope.Data) == 0 || !acknowledgement && envelope.Data[0] != '{' || json.Unmarshal(envelope.Data, result) != nil {
		return errors.New("invalid reference-upload response")
	}
	return nil
}

func jsonCall(ctx context.Context, do Do, path string, value, result any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw, err := do(ctx, http.MethodPost, Root+path, map[string]string{"Content-Type": "application/json"}, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	return decode(raw, result)
}

func validateCompleted(state *State) (map[int]string, error) {
	completed := make(map[int]string, len(state.Completed))
	for _, part := range state.Completed {
		if state.Upload == nil || state.Upload.Mode != "multipart" || part.Number < 1 || part.Number > len(state.Upload.Parts) || len(part.ETag) == 0 || len(part.ETag) > 512 || strings.ContainsAny(part.ETag, "\r\n\x00") || completed[part.Number] != "" {
			return nil, errors.New("invalid persisted upload parts")
		}
		completed[part.Number] = part.ETag
	}
	return completed, nil
}

// Ensure resumes an upload using a verified, bounded host file. It deliberately
// uses the authenticated upstream relay: returned storage URLs are never fetched
// and the upstream key can never leak to a storage destination.
func Ensure(ctx context.Context, file File, source io.ReaderAt, state *State, do Do, save Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Validate(); err != nil {
		return permanent(err)
	}
	if state.Reference != nil {
		if err := state.Reference.Validate(file); err != nil {
			return permanent(err)
		}
		return nil
	}
	if state.Upload == nil {
		var reply struct {
			Disposition string     `json:"disposition"`
			Ticket      string     `json:"ticket"`
			RetryAfter  float64    `json:"retry_after_seconds"`
			Upload      *Upload    `json:"upload"`
			Reference   *Reference `json:"reference"`
		}
		if err := jsonCall(ctx, do, "", file, &reply); err != nil {
			return err
		}
		// A stable upload idempotency key is used again after a wait/restart.
		// The ticket is not retained in logs or a public task response.
		for n := 0; reply.Disposition == "waiting"; n++ {
			if n >= 1 {
				delay := time.Duration(reply.RetryAfter * float64(time.Second))
				if delay < time.Second || delay > 15*time.Second {
					delay = 15 * time.Second
				}
				return &Waiting{Delay: delay}
			}
			if reply.Ticket == "" || len(reply.Ticket) > 4096 {
				return permanent(errors.New("invalid upload wait ticket"))
			}
			delay := time.Duration(reply.RetryAfter * float64(time.Second))
			if delay < time.Second {
				delay = time.Second
			}
			if delay > 15*time.Second {
				delay = 15 * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			ticket := reply.Ticket
			reply = struct {
				Disposition string     `json:"disposition"`
				Ticket      string     `json:"ticket"`
				RetryAfter  float64    `json:"retry_after_seconds"`
				Upload      *Upload    `json:"upload"`
				Reference   *Reference `json:"reference"`
			}{}
			if err := jsonCall(ctx, do, "/wait", map[string]string{"ticket": ticket}, &reply); err != nil {
				return err
			}
		}
		switch reply.Disposition {
		case "reused":
			if reply.Reference == nil {
				return permanent(errors.New("missing reused reference"))
			}
			if err := reply.Reference.Validate(file); err != nil {
				return permanent(err)
			}
			state.Reference = reply.Reference
			return save(ctx)
		case "owner":
			if err := reply.Upload.Validate(file.Size); err != nil {
				return permanent(err)
			}
			state.Upload = reply.Upload
			state.Completed = nil
			state.Relayed = false
			if err := save(ctx); err != nil {
				return err
			}
		default:
			return permanent(errors.New("unknown reference-upload disposition"))
		}
	}
	if err := state.Upload.Validate(file.Size); err != nil {
		return permanent(err)
	}
	completed, err := validateCompleted(state)
	if err != nil {
		return permanent(err)
	}
	parts := state.Upload.Parts
	if state.Upload.Mode == "single" {
		parts = []Part{{Number: 0, Size: file.Size}}
	}
	var offset int64
	for _, part := range parts {
		start := offset
		offset += part.Size
		if completed[part.Number] != "" || state.Upload.Mode == "single" && state.Relayed {
			continue
		}
		headers := map[string]string{"Content-Type": "application/octet-stream", "X-Upload-Session": state.Upload.Session, "X-Upload-Generation": strconv.FormatInt(state.Upload.Generation, 10), "X-Upload-Part": strconv.Itoa(part.Number)}
		raw, err := do(ctx, http.MethodPut, Root+"/relay", headers, io.NewSectionReader(source, start, part.Size), part.Size)
		if err != nil {
			return err
		}
		var relayed struct {
			ETag string `json:"etag"`
		}
		if err := decode(raw, &relayed); err != nil {
			return err
		}
		if state.Upload.Mode == "multipart" {
			if len(relayed.ETag) == 0 || len(relayed.ETag) > 512 || strings.ContainsAny(relayed.ETag, "\r\n\x00") {
				return permanent(fmt.Errorf("missing or invalid upload part ETag"))
			}
			state.Completed = append(state.Completed, CompletedPart{Number: part.Number, ETag: relayed.ETag})
		} else {
			state.Relayed = true
		}
		if err := save(ctx); err != nil {
			return err
		}
	}
	var reference Reference
	if err := jsonCall(ctx, do, "/complete", struct {
		Session    string          `json:"session_token"`
		Generation int64           `json:"generation"`
		Parts      []CompletedPart `json:"parts,omitempty"`
	}{state.Upload.Session, state.Upload.Generation, state.Completed}, &reference); err != nil {
		return err
	}
	if err := reference.Validate(file); err != nil {
		return permanent(err)
	}
	state.Reference, state.Upload, state.Completed = &reference, nil, nil
	state.Relayed = false
	return save(ctx)
}

// Abort is repeatable. Completed objects have no public delete contract and are
// never deleted speculatively; their lifetime belongs to the upstream storage.
func Abort(ctx context.Context, state *State, do Do, save Checkpoint) error {
	if state.Upload == nil {
		return nil
	}
	var reply any
	if err := jsonCall(ctx, do, "/abort", struct {
		Session    string `json:"session_token"`
		Generation int64  `json:"generation"`
	}{state.Upload.Session, state.Upload.Generation}, &reply); err != nil {
		return err
	}
	state.Upload, state.Completed = nil, nil
	state.Relayed = false
	return save(ctx)
}
