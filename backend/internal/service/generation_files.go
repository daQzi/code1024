package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
)

const GenerationFileChunkBytes = 64 << 10
const GenerationFileUserQuota int64 = 128 << 20
const GenerationFileGlobalQuota int64 = 2 << 30
const GenerationFileUserCount = 64
const GenerationFileGlobalCount = 4096

var ErrGenerationFileNotFound = infraerrors.New(404, "VIDEO_FILE_NOT_FOUND", "Video file not found or expired")
var ErrGenerationFileQuota = infraerrors.New(429, "VIDEO_FILE_QUOTA", "Video file storage limit reached; delete unused files or retry after expiry")
var ErrGenerationFileTooLarge = infraerrors.New(413, "VIDEO_FILE_TOO_LARGE", "Video files must contain 1 byte to 32 MiB")
var ErrGenerationFileBusy = infraerrors.New(503, "VIDEO_FILE_BUSY", "Video upload capacity is temporarily full; retry later")
var ErrGenerationFileInUse = infraerrors.New(409, "VIDEO_FILE_IN_USE", "Video file is retained by an accepted task")
var generationFileIDPattern = regexp.MustCompile(`^vfile_[a-f0-9]{40}$`)

type GenerationFileSnapshot = generation.FileSnapshot
type GenerationFile = generation.File

type GenerationFileRepository interface {
	StoreGenerationFile(context.Context, int64, int64, int64, string, string, io.ReadSeeker) (*GenerationFile, error)
	GetGenerationFile(context.Context, string, int64, int64, int64) (*GenerationFile, error)
	OpenGenerationFile(context.Context, string, int64, int64, int64) (io.ReadCloser, error)
	DeleteGenerationFile(context.Context, string, int64, int64, int64) error
	CleanupGenerationFiles(context.Context, int) (int64, error)
}

func NewGenerationFileID() (string, error) { return generation.NewFileID() }

func ValidateGenerationFilename(name, contentType string) (string, string, error) {
	return generation.ValidateFilename(name, contentType)
}

func (s *GenerationService) fileRepository() GenerationFileRepository {
	if s == nil {
		return nil
	}
	repo, _ := s.repo.(GenerationFileRepository)
	return repo
}

func (s *GenerationService) HasVideoFileStorage() bool { return s.fileRepository() != nil }

func (s *GenerationService) GetVideoFile(ctx context.Context, key *APIKey, id string) (*GenerationFile, error) {
	repo := s.fileRepository()
	if repo == nil || key == nil || key.GroupID == nil {
		return nil, ErrGenerationFileNotFound
	}
	return repo.GetGenerationFile(ctx, id, key.UserID, key.ID, *key.GroupID)
}

func (s *GenerationService) DeleteVideoFile(ctx context.Context, key *APIKey, id string) error {
	repo := s.fileRepository()
	if repo == nil || key == nil || key.GroupID == nil {
		return ErrGenerationFileNotFound
	}
	return repo.DeleteGenerationFile(ctx, id, key.UserID, key.ID, *key.GroupID)
}

// UploadGenerationFile spools at most four bounded inputs per process before
// entering a DB transaction. Slow clients never hold DB locks or connections.
func (s *GenerationService) UploadGenerationFile(ctx context.Context, key *APIKey, filename, kind string, body io.Reader) (*GenerationFile, error) {
	repo := s.fileRepository()
	if repo == nil || key == nil || key.GroupID == nil {
		return nil, errors.New("video file service unavailable")
	}
	filename, kind, err := ValidateGenerationFilename(filename, kind)
	if err != nil {
		return nil, err
	}
	select {
	case generationUploadSlots <- struct{}{}:
		defer func() { <-generationUploadSlots }()
	default:
		return nil, ErrGenerationFileBusy
	}
	file, err := newGenerationSpool()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	n, err := io.CopyBuffer(file, io.LimitReader(body, pluginTask.MaxFileBytes+1), make([]byte, 32<<10))
	if err != nil {
		return nil, err
	}
	if n <= 0 || n > pluginTask.MaxFileBytes {
		return nil, ErrGenerationFileTooLarge
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return repo.StoreGenerationFile(ctx, key.UserID, key.ID, *key.GroupID, filename, kind, file)
}

var generationUploadSlots = make(chan struct{}, 4)

// On the supported Unix hosts, unlink immediately: the open descriptor still
// works, and both cancellation and a process crash release the temporary data.
// A host that cannot unlink an open file rejects spooling instead of retaining
// private, untracked files across restarts.
func newGenerationSpool() (*os.File, error) {
	file, err := os.CreateTemp("", "sub2api-video-spool-*")
	if err != nil {
		return nil, err
	}
	if err = os.Remove(file.Name()); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	return file, nil
}

type generationDecodeFilesKey struct{}
type generationMultipartInputKey struct{}

func WithGenerationMultipartInput(ctx context.Context) context.Context {
	return context.WithValue(ctx, generationMultipartInputKey{}, true)
}

func (s *GenerationService) prepareVideoFiles(ctx context.Context, key *APIKey, body json.RawMessage, protocol string) (json.RawMessage, []GenerationFileSnapshot, error) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal(body, &input); err != nil {
		return nil, nil, err
	}
	if upload, _ := ctx.Value(generationMultipartUploadKey{}).(*generationMultipartUpload); upload != nil && len(upload.files) > 0 {
		if (protocol != GenerationProtocolOpenAI && protocol != GenerationProtocolGenericTask && protocol != GenerationProtocolOpenAIImage && !strings.HasPrefix(protocol, GenerationProtocolNativePrefix)) || input["file_references"] != nil {
			return nil, nil, errors.New("multipart files conflict with the request")
		}
		for _, file := range upload.files {
			if input[file.field] != nil {
				return nil, nil, errors.New("file and text field conflict")
			}
		}
		files, err := s.persistVideoMultipart(ctx, key, upload)
		return body, files, err
	}
	raw, exists := input["file_references"]
	if !exists {
		multipart, _ := ctx.Value(generationMultipartInputKey{}).(bool)
		if multipart && protocol == GenerationProtocolOpenAI {
			var reference string
			if json.Unmarshal(input["input_reference"], &reference) == nil && strings.HasPrefix(reference, "data:") {
				header, encoded, ok := strings.Cut(strings.TrimPrefix(reference, "data:"), ",")
				kind, isBase64 := strings.CutSuffix(header, ";base64")
				if !ok || !isBase64 || kind != "image/png" && kind != "image/jpeg" && kind != "image/webp" {
					return nil, nil, errors.New("invalid multipart image")
				}
				file, err := s.UploadGenerationFile(ctx, key, "input_reference", kind, base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)))
				if err != nil {
					return nil, nil, err
				}
				delete(input, "input_reference")
				cleaned, err := json.Marshal(input)
				files := []GenerationFileSnapshot{{ID: file.ID, SHA256: file.SHA256, File: pluginTask.File{Ref: "request_file:input_reference", Field: "input_reference", Filename: file.Filename, MimeType: file.MimeType, Size: file.Size}}}
				return cleaned, files, err
			}
		}
		return body, nil, nil
	}
	if !generationProtocolAllowsFileReferences(protocol) {
		return nil, nil, errors.New("file_references is only supported by the OpenAI Videos extension")
	}
	if key == nil || key.GroupID == nil {
		return nil, nil, errors.New("video file identity unavailable")
	}
	if pinned, _ := ctx.Value(videoPolicyFilesKey{}).(*videoPolicyFiles); pinned != nil {
		if !pinned.matches(key, string(raw)) {
			return nil, nil, ErrGenerationFileNotFound
		}
		for _, file := range pinned.snapshots {
			if _, collision := input[file.Field]; collision {
				return nil, nil, errors.New("file and text field conflict")
			}
		}
		delete(input, "file_references")
		cleaned, err := json.Marshal(input)
		return cleaned, slices.Clone(pinned.snapshots), err
	}
	var bindings []struct {
		ID    string `json:"file_id"`
		Field string `json:"field"`
	}
	if json.Unmarshal(raw, &bindings) != nil || len(bindings) == 0 || len(bindings) > pluginTask.MaxTaskFiles {
		return nil, nil, errors.New("file_references must contain between 1 and 4 file bindings")
	}
	repo := s.fileRepository()
	if repo == nil {
		return nil, nil, errors.New("video file service unavailable")
	}
	files := make([]GenerationFileSnapshot, 0, len(bindings))
	counts := map[string]int{}
	for _, binding := range bindings {
		if !generationFileIDPattern.MatchString(binding.ID) || !pluginTask.ValidVideoFileField(binding.Field) {
			return nil, nil, errors.New("invalid file reference or file field")
		}
		if _, duplicate := input[binding.Field]; duplicate {
			return nil, nil, errors.New("file and text field conflict")
		}
		file, err := repo.GetGenerationFile(ctx, binding.ID, key.UserID, key.ID, *key.GroupID)
		if err != nil {
			return nil, nil, err
		}
		ref := generationFileRef(binding.Field, counts[binding.Field])
		counts[binding.Field]++
		files = append(files, GenerationFileSnapshot{ID: file.ID, SHA256: file.SHA256, File: pluginTask.File{Ref: ref, Field: binding.Field, Filename: file.Filename, MimeType: file.MimeType, Size: file.Size}})
	}
	delete(input, "file_references")
	cleaned, err := json.Marshal(input)
	return cleaned, files, err
}

func generationFileRef(field string, ordinal int) string { return generation.FileRef(field, ordinal) }

// generationContextDecodeFiles builds read-only opaque file metadata for
// pre-admission native route decoding when a request carries spooled multipart
// files or validated pre-uploaded file_references. Local paths and raw file
// bytes are never exposed.
func generationContextDecodeFiles(ctx context.Context, body json.RawMessage) (json.RawMessage, []GenerationFileSnapshot) {
	if upload, _ := ctx.Value(generationMultipartUploadKey{}).(*generationMultipartUpload); upload != nil && len(upload.files) > 0 {
		files := make([]GenerationFileSnapshot, 0, len(upload.files))
		counts := map[string]int{}
		for _, input := range upload.files {
			ref := generationFileRef(input.field, counts[input.field])
			counts[input.field]++
			files = append(files, GenerationFileSnapshot{
				File: pluginTask.File{Ref: ref, Field: input.field, Filename: input.filename, MimeType: input.kind, Size: input.size},
			})
		}
		return body, files
	}
	if pinned, _ := ctx.Value(videoPolicyFilesKey{}).(*videoPolicyFiles); pinned != nil && len(pinned.snapshots) > 0 {
		var obj map[string]json.RawMessage
		if json.Unmarshal(body, &obj) == nil && obj != nil {
			delete(obj, "file_references")
			if cleaned, err := json.Marshal(obj); err == nil {
				return cleaned, slices.Clone(pinned.snapshots)
			}
		}
		return body, slices.Clone(pinned.snapshots)
	}
	return body, nil
}

func generationFileDecodeBody(body json.RawMessage, files []GenerationFileSnapshot) (map[string]any, error) {
	if len(files) == 0 {
		return map[string]any{"kind": "json", "value": body}, nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(body, &input); err != nil {
		return nil, err
	}
	fields := map[string][]string{}
	for name, raw := range input {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			text = string(raw)
		}
		fields[name] = []string{text}
	}
	metadata := make([]pluginTask.File, 0, len(files))
	for _, file := range files {
		metadata = append(metadata, file.File)
	}
	return map[string]any{"kind": "multipart", "fields": fields, "files": metadata}, nil
}

func (s *GenerationService) workGenerationFileCleanup(ctx context.Context, repo GenerationFileRepository) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := repo.CleanupGenerationFiles(opCtx, 20)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("generation_file_cleanup", "error_type", fmt.Sprintf("%T", err))
		}
	}
}
