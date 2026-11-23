package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/newapi/mediahost"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"github.com/tidwall/gjson"
)

type videoPolicyFilesKey struct{}

type videoPolicyFiles struct {
	userID, tokenID, groupID int64
	references               string
	snapshots                []GenerationFileSnapshot
}

func generationProtocolAllowsFileReferences(protocol string) bool {
	switch protocol {
	case GenerationProtocolOpenAI, GenerationProtocolGenericTask, GenerationProtocolOpenAIResponses, GenerationProtocolOpenAIImage:
		return true
	default:
		return strings.HasPrefix(protocol, GenerationProtocolNativePrefix)
	}
}

// PrepareVideoPolicyFiles projects private preuploads after the idempotency
// check, before any ingress hook can observe them. Only metadata is loaded.
// A later admission reuses these exact snapshots; CreateHeld still validates
// ownership, expiry, metadata and bytes identity in its binding transaction.
func (s *GenerationService) PrepareVideoPolicyFiles(ctx context.Context, key *APIKey, body []byte, protocol string) (context.Context, error) {
	ref := gjson.GetBytes(body, "file_references")
	if !ref.Exists() || !generationProtocolAllowsFileReferences(protocol) {
		return ctx, nil
	}
	if key == nil || key.GroupID == nil || !newAPIProviderUnambiguousJSON(gjson.ParseBytes(body), 0) || HasGenerationMultipartFile(ctx) {
		return ctx, ErrGenerationFileNotFound
	}
	if pinned, _ := ctx.Value(videoPolicyFilesKey{}).(*videoPolicyFiles); pinned != nil {
		if !pinned.matches(key, ref.Raw) {
			return ctx, ErrGenerationFileNotFound
		}
		return ctx, nil
	}
	_, files, err := s.prepareVideoFiles(ctx, key, body, protocol)
	if err != nil {
		return ctx, err
	}
	pinned := &videoPolicyFiles{userID: key.UserID, tokenID: key.ID, groupID: *key.GroupID, references: ref.Raw, snapshots: files}
	return context.WithValue(ctx, videoPolicyFilesKey{}, pinned), nil
}

func (p *videoPolicyFiles) matches(key *APIKey, references string) bool {
	if key == nil || key.GroupID == nil || p.userID != key.UserID || p.tokenID != key.ID || p.groupID != *key.GroupID {
		return false
	}
	// Patches may reserialize the surrounding JSON, including the untouched
	// references. Match the decoded IDs and field order, rather than spaces.
	return equalVideoPolicyRefs(p.references, references)
}

func equalVideoPolicyRefs(before, after string) bool {
	type binding struct {
		ID    string `json:"file_id"`
		Field string `json:"field"`
	}
	var a, b []binding
	if before == after {
		return true
	}
	if json.Unmarshal([]byte(before), &a) != nil || json.Unmarshal([]byte(after), &b) != nil || len(a) != len(b) {
		return false
	}
	for i, left := range a {
		if left != b[i] {
			return false
		}
	}
	return true
}

func requestPluginVideoSource(ctx context.Context, body []byte, cache *mediahost.TextCache) (*mediahost.Source, error) {
	if pinned, _ := ctx.Value(videoPolicyFilesKey{}).(*videoPolicyFiles); pinned != nil {
		var input map[string]json.RawMessage
		if err := json.Unmarshal(body, &input); err != nil || !equalVideoPolicyRefs(pinned.references, string(input["file_references"])) {
			return nil, errors.New("video file bindings changed before policy execution")
		}
		delete(input, "file_references")
		body, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		files := make([]mediahost.MultipartFile, 0, len(pinned.snapshots))
		for _, file := range pinned.snapshots {
			files = append(files, mediahost.MultipartFile{Field: file.Field, Filename: file.Filename, MIME: file.MimeType, Size: int(file.Size)})
		}
		return mediahost.NewWithMultipartFiles(body, cache, files)
	}
	return generationMultipartPolicySource(ctx, body, cache)
}

func checkVideoPolicyReferenceEffects(effects []requestpatch.Effect) error {
	for _, effect := range effects {
		for _, op := range effect.Ops {
			if protectedVideoPolicyPointer(op.Path) || (op.Op == "copy" || op.Op == "move") && protectedVideoPolicyPointer(op.From) {
				return errors.New("video file bindings are protected")
			}
		}
	}
	return nil
}

func protectedVideoPolicyPointer(pointer string) bool {
	if pointer == "" {
		return true
	}
	root, _, _ := strings.Cut(strings.TrimPrefix(pointer, "/"), "/")
	return strings.EqualFold(root, "file_references")
}
