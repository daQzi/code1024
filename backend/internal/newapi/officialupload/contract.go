// Package officialupload implements the documented Sub2API reference-upload
// protocol. File bytes stay in the Go host; scripts only see opaque references.
package officialupload

import (
	"encoding/hex"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
)

const MaxParts = 128

type File struct {
	SHA256          string  `json:"sha256"`
	Size            int64   `json:"size_bytes"`
	Kind            string  `json:"kind"`
	ContentType     string  `json:"content_type"`
	IdempotencyKey  string  `json:"idempotency_key"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
}

type Reference struct {
	ObjectID string `json:"storage_object_id"`
	Kind     string `json:"kind"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size_bytes"`
}

type Part struct {
	Number int   `json:"part_number"`
	Size   int64 `json:"size_bytes"`
}

type CompletedPart struct {
	Number int    `json:"part_number"`
	ETag   string `json:"etag"`
}

type Upload struct {
	Session    string `json:"session_token"`
	Generation int64  `json:"generation"`
	Mode       string `json:"mode"`
	Parts      []Part `json:"parts,omitempty"`
}

// State contains upstream upload credentials and must be encrypted at rest.
// Checkpoint the owner session before sending bytes, and every completed part.
type State struct {
	Upload    *Upload         `json:"upload,omitempty"`
	Completed []CompletedPart `json:"completed,omitempty"`
	Reference *Reference      `json:"reference,omitempty"`
	Relayed   bool            `json:"relayed,omitempty"`
}

var objectID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,512}$`)

func (f File) Validate() error {
	digest, err := hex.DecodeString(f.SHA256)
	if err != nil || len(digest) != 32 || f.Size < 1 || f.Size > 32<<20 || len(f.IdempotencyKey) < 1 || len(f.IdempotencyKey) > 200 {
		return errors.New("invalid upload file identity")
	}
	if f.Kind != "image" && f.Kind != "video" && f.Kind != "audio" || !strings.HasPrefix(f.ContentType, f.Kind+"/") || len(f.ContentType) > 128 || strings.ContainsAny(f.ContentType, "\r\n;\x00") {
		return errors.New("invalid upload media type")
	}
	if math.IsNaN(f.DurationSeconds) || math.IsInf(f.DurationSeconds, 0) || f.DurationSeconds < 0 || f.DurationSeconds > 86400 || f.Kind != "image" && f.DurationSeconds <= 0 {
		return errors.New("video and audio uploads require their actual duration")
	}
	return nil
}

func (r Reference) Validate(f File) error {
	if !objectID.MatchString(r.ObjectID) || r.Kind != f.Kind || !strings.EqualFold(r.SHA256, f.SHA256) || r.Size != f.Size {
		return errors.New("upstream reference does not match the bound file")
	}
	return nil
}

func (u *Upload) Validate(size int64) error {
	if u == nil || len(u.Session) < 1 || len(u.Session) > 4096 || strings.ContainsAny(u.Session, "\r\n\x00") || u.Generation < 1 || u.Generation > 9007199254740991 {
		return errors.New("invalid upload session")
	}
	if u.Mode == "single" && len(u.Parts) == 0 {
		return nil
	}
	if u.Mode != "multipart" || len(u.Parts) < 1 || len(u.Parts) > MaxParts {
		return errors.New("invalid upload mode or parts")
	}
	slices.SortFunc(u.Parts, func(a, b Part) int { return a.Number - b.Number })
	var total int64
	for i, part := range u.Parts {
		if part.Number != i+1 || part.Size <= 0 || part.Size > size-total {
			return errors.New("invalid upload part boundaries")
		}
		total += part.Size
	}
	if total != size {
		return errors.New("upload parts do not cover the bound file")
	}
	return nil
}
