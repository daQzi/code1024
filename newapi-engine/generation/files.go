package generation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"mime"
	"regexp"
	"strconv"
	"strings"
	"time"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
)

var fileIDPattern = regexp.MustCompile(`^vfile_[a-f0-9]{40}$`)

// FileSnapshot records only a reference. File content is kept in
// bounded private chunks, independently of the task's JSON and JS heap.
type FileSnapshot struct {
	pluginTask.File
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

type File struct {
	ID        string    `json:"id"`
	Object    string    `json:"object"`
	Filename  string    `json:"filename"`
	MimeType  string    `json:"mime_type"`
	Size      int64     `json:"bytes"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func NewFileID() (string, error) {
	var value [20]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "vfile_" + hex.EncodeToString(value[:]), nil
}

func ValidateFilename(name, contentType string) (string, string, error) {
	if name == "" {
		name = "reference"
	}
	kind, params, err := mime.ParseMediaType(contentType)
	if err != nil || len(params) != 0 || len(kind) > 128 || !strings.HasPrefix(kind, "image/") && !strings.HasPrefix(kind, "video/") && !strings.HasPrefix(kind, "audio/") {
		return "", "", errors.New("video files require an image, video or audio MIME type without parameters")
	}
	if len(name) > 255 || strings.ContainsAny(name, "\r\n\x00/\\") {
		return "", "", errors.New("invalid filename")
	}
	return name, kind, nil
}

func FileRef(field string, ordinal int) string {
	ref := "request_file:" + field
	if ordinal > 0 {
		ref += ":" + strconv.Itoa(ordinal)
	}
	return ref
}
