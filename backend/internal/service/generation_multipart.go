package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/mediahost"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/tidwall/gjson"
)

// Multiple input files are a site extension of the standard Videos envelope.
// The decoder decides which fields/counts the upstream actually supports.
const GenerationMultipartMaxBytes int64 = pluginTask.MaxMultipartBytes

type generationMultipartUploadKey struct{}
type generationMultipartBodyKey struct{}

// Request-owned only: nothing holding this descriptor is queued or passed to
// JS. prepareVideoFiles persists it before CreateHeld accepts a durable task.
type generationMultipartUpload struct {
	files []*generationMultipartFile
}

type generationMultipartFile struct {
	file     *os.File
	field    string
	filename string
	kind     string
	size     int64
	sha256   [sha256.Size]byte
	storedID string
}

type generationUploadReader struct {
	ctx context.Context
	io.Reader
}

func (r generationUploadReader) Read(dst []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(dst)
}

func HasGenerationMultipartFile(ctx context.Context) bool {
	upload, _ := ctx.Value(generationMultipartUploadKey{}).(*generationMultipartUpload)
	return upload != nil && len(upload.files) > 0
}

// ReadVideoMultipart spools under the same four upload slots as raw uploads.
// It computes replay identity before touching storage or the current price.
// The caller must reserve shared ingress memory and impose a read deadline.
func (s *GenerationService) ReadVideoMultipart(ctx context.Context, key *APIKey, contentType string, source io.Reader) (context.Context, json.RawMessage, func(), error) {
	if err := ctx.Err(); err != nil {
		return ctx, nil, nil, err
	}
	select {
	case generationUploadSlots <- struct{}{}:
	default:
		return ctx, nil, nil, ErrGenerationFileBusy
	}
	upload := &generationMultipartUpload{}
	cleanupContext := ctx
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupContext), 5*time.Second)
			defer cancel()
			for _, file := range upload.files {
				if file.file != nil {
					_ = file.file.Close()
				}
				// Failed admission and concurrent idempotent losers leave no orphan
				// quota consumption. Accepted bindings refuse deletion by design.
				if file.storedID != "" {
					err := s.DeleteVideoFile(cleanupCtx, key, file.storedID)
					if err != nil && !errors.Is(err, ErrGenerationFileInUse) && !errors.Is(err, ErrGenerationFileNotFound) {
						slog.Warn("generation_multipart_cleanup", "error_type", fmt.Sprintf("%T", err))
					}
				}
			}
			<-generationUploadSlots
		})
	}
	limited := &io.LimitedReader{R: generationUploadReader{ctx: ctx, Reader: source}, N: GenerationMultipartMaxBytes + 1}
	original, err := pluginTask.ParseVideoMultipart(contentType, limited, func(field, filename string, input io.Reader) (any, error) {
		file, err := newGenerationSpool()
		if err != nil {
			return nil, err
		}
		inputFile := &generationMultipartFile{file: file, field: field}
		upload.files = append(upload.files, inputFile)
		digest := sha256.New()
		size, err := io.CopyBuffer(io.MultiWriter(file, digest), io.LimitReader(input, pluginTask.MaxFileBytes+1), make([]byte, 32<<10))
		if err != nil {
			return nil, err
		}
		if size == 0 || size > pluginTask.MaxFileBytes {
			return nil, ErrGenerationFileTooLarge
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		var header [512]byte
		n, err := io.ReadFull(file, header[:])
		if err != nil && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		kind := http.DetectContentType(header[:n])
		if field == "input_reference" && kind != "image/png" && kind != "image/jpeg" && kind != "image/webp" {
			return nil, errors.New("input_reference must be a PNG, JPEG or WebP image")
		}
		filename, kind, err = ValidateGenerationFilename(filename, kind)
		if err != nil {
			return nil, err
		}
		inputFile.filename, inputFile.kind, inputFile.size = filename, kind, size
		copy(inputFile.sha256[:], digest.Sum(nil))
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		// Stable across boundaries, order and filenames. File bytes never
		// become JSON or enter the JS worker's heap.
		return map[string]any{"sha256": hex.EncodeToString(inputFile.sha256[:]), "bytes": size, "mime_type": kind}, nil
	})
	if err == nil {
		// Include epilogue bytes in the total limit; a completed multipart
		// boundary must not hide an unlimited trailing request body.
		_, err = io.Copy(io.Discard, limited)
	}
	if limited.N == 0 {
		err = ErrGenerationFileTooLarge
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cleanup()
		return ctx, nil, nil, err
	}
	// Field order does not participate in the normalized fingerprint. Present
	// files to decoders in the same canonical order, preserving order within a
	// repeated field where first/last-frame meaning may depend on it.
	sort.SliceStable(upload.files, func(i, j int) bool { return upload.files[i].field < upload.files[j].field })
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(original, &fields); err != nil {
		cleanup()
		return ctx, nil, nil, err
	}
	if len(upload.files) > 0 {
		if _, conflict := fields["file_references"]; conflict {
			cleanup()
			return ctx, nil, nil, errors.New("multipart files and file_references cannot be combined")
		}
		// Preserve the old single-small-image identity without ever building
		// a data URL for every file in a multiple-file request.
		if len(upload.files) == 1 && upload.files[0].field == "input_reference" && upload.files[0].size <= pluginTask.MaxInlineFileBytes {
			file := upload.files[0]
			data, readErr := io.ReadAll(io.LimitReader(file.file, pluginTask.MaxInlineFileBytes+1))
			if readErr != nil || int64(len(data)) != file.size {
				cleanup()
				if readErr == nil {
					readErr = errors.New("temporary file size changed")
				}
				return ctx, nil, nil, readErr
			}
			fields[file.field], err = json.Marshal("data:" + file.kind + ";base64," + base64.StdEncoding.EncodeToString(data))
			if err == nil {
				original, err = json.Marshal(fields)
			}
			if err != nil {
				cleanup()
				return ctx, nil, nil, err
			}
		}
		ctx = context.WithValue(ctx, generationMultipartUploadKey{}, upload)
	}
	ctx, err = WithGenerationClientRequest(ctx, original)
	if err != nil {
		cleanup()
		return ctx, nil, nil, err
	}
	for _, file := range upload.files {
		delete(fields, file.field)
	}
	body, err := json.Marshal(fields)
	if err != nil {
		cleanup()
		return ctx, nil, nil, err
	}
	return context.WithValue(ctx, generationMultipartBodyKey{}, true), body, cleanup, nil
}

func generationMultipartPolicySource(ctx context.Context, body []byte, cache *mediahost.TextCache) (*mediahost.Source, error) {
	upload, _ := ctx.Value(generationMultipartUploadKey{}).(*generationMultipartUpload)
	if upload == nil {
		return mediahost.NewWithCache(body, cache)
	}
	files := make([]mediahost.MultipartFile, 0, len(upload.files))
	for _, file := range upload.files {
		files = append(files, mediahost.MultipartFile{
			Field: file.field, Filename: file.filename, MIME: file.kind, Size: int(file.size), SHA256: file.sha256,
			Open: func(ctx context.Context) (io.ReadCloser, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return io.NopCloser(io.NewSectionReader(file.file, 0, file.size)), nil
			},
		})
	}
	return mediahost.NewWithMultipartFiles(body, cache, files)
}

// Follow the pinned New API multipart rule: replacements of existing text
// fields only. Videos metadata is already a JSON object, so it stays fixed.
func checkGenerationMultipartPolicyEffects(body []byte, effects []requestpatch.Effect) error {
	parsed := gjson.ParseBytes(body)
	for _, effect := range effects {
		if effect.Type != requestpatch.EffectJSONPatch {
			return errors.New("video multipart only accepts text field replacements")
		}
		for _, op := range effect.Ops {
			valid := false
			parsed.ForEach(func(key, value gjson.Result) bool {
				pointer := "/" + strings.ReplaceAll(strings.ReplaceAll(key.Str, "~", "~0"), "/", "~1")
				if op.Path != pointer {
					return true
				}
				updated := gjson.ParseBytes(op.Value)
				valid = !strings.EqualFold(key.Str, "model") && !strings.EqualFold(key.Str, "stream") && !strings.EqualFold(key.Str, "file_references") &&
					op.Op == "replace" && value.Type == gjson.String && updated.Type == gjson.String && len(updated.Str) <= 512<<10
				return false
			})
			if !valid {
				return errors.New("video multipart field layout or identity changed")
			}
		}
	}
	return nil
}

func (s *GenerationService) persistVideoMultipart(ctx context.Context, key *APIKey, upload *generationMultipartUpload) ([]GenerationFileSnapshot, error) {
	repo := s.fileRepository()
	if repo == nil || key == nil || key.GroupID == nil || len(upload.files) == 0 {
		return nil, errors.New("video multipart storage unavailable")
	}
	for _, input := range upload.files {
		if input.storedID != "" {
			return nil, errors.New("multipart files were already persisted")
		}
	}
	files := make([]GenerationFileSnapshot, 0, len(upload.files))
	counts := map[string]int{}
	for _, input := range upload.files {
		if _, err := input.file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		file, err := repo.StoreGenerationFile(ctx, key.UserID, key.ID, *key.GroupID, input.filename, input.kind, input.file)
		if err != nil {
			return nil, err
		}
		input.storedID = file.ID
		ref := generationFileRef(input.field, counts[input.field])
		counts[input.field]++
		files = append(files, GenerationFileSnapshot{ID: file.ID, SHA256: file.SHA256, File: pluginTask.File{Ref: ref, Field: input.field, Filename: file.Filename, MimeType: file.MimeType, Size: file.Size}})
	}
	return files, nil
}
