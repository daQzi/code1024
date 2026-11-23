// SPDX-License-Identifier: AGPL-3.0-or-later
// Multipart attachment projection follows the pinned New API attachments
// contract. The caller owns parsing, admission and the lifetime of each file.
package mediahost

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

// MultipartFile is a request-owned file, identified by its validated content
// digest. Open must return a fresh reader over the immutable bytes for this
// request only. If Open is nil, the attachment exposes metadata only. No
// user-supplied path or opaque storage ID is passed to the source.
type MultipartFile struct {
	Field, Filename, MIME string
	Size                  int
	SHA256                [sha256.Size]byte
	Open                  func(context.Context) (io.ReadCloser, error)
}

func NewWithMultipartFiles(body []byte, cache *TextCache, files []MultipartFile, options ...Option) (*Source, error) {
	s, err := NewWithCache(body, cache, options...)
	if err != nil {
		return nil, err
	}
	if len(files)+len(s.items) > 64 || len(files)+len(s.attachments) > 64 {
		return nil, errors.New("multipart attachment count exceeds limit")
	}
	for _, file := range files {
		if file.Field == "" || file.Size <= 0 || file.Open != nil && file.SHA256 == [sha256.Size]byte{} {
			return nil, errors.New("invalid multipart attachment")
		}
		ref := fmt.Sprintf("[media:%d]", len(s.items))
		s.items = append(s.items, item{path: file.Field, filename: file.Filename, mime: file.MIME, size: file.Size, readonly: true, metadataOnly: file.Open == nil, open: file.Open, digest: file.SHA256})
		s.attachments = append(s.attachments, jsplugin.Attachment{Ref: ref, Kind: kind(file.MIME, file.Filename), Mime: file.MIME, Size: file.Size, Filename: file.Filename, Path: file.Field})
	}
	return s, nil
}
