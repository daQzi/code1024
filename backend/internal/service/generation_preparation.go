package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/officialupload"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/google/uuid"
)

const maxGenerationPreparation = 64 << 10

type generationUploadReference struct {
	Ref      string  `json:"file_ref"`
	Type     string  `json:"type"`
	Duration float64 `json:"duration_seconds"`
}

type generationUploadFile struct {
	Ref   string               `json:"ref"`
	File  officialupload.File  `json:"file"`
	State officialupload.State `json:"state"`
}

// Includes the original credential for aborting an unfinished upload after key
// rotation/deletion. Never expose this envelope through task or admin views.
type generationPreparation struct {
	TaskID    string                 `json:"task_id"`
	AccountID int64                  `json:"account_id"`
	VersionID int64                  `json:"version_id"`
	Identity  string                 `json:"identity"`
	Base      string                 `json:"base"`
	APIKey    string                 `json:"api_key"`
	Files     []generationUploadFile `json:"files"`
}

func generationPreparationIdentity(task *GenerationTask) string {
	raw, _ := json.Marshal([]any{task.ID, task.UserID, task.APIKeyID, task.GroupID, task.AccountID, task.PluginVersionID, task.Request, task.Files, task.Connection})
	return HashUsageRequestPayload(raw)
}

// Only the reviewed, shipped revision can opt into this host extension. Merely
// choosing the same plugin key/name does not grant a community script access.
func officialFiveUploadVersion(version *NewAPIPluginVersion) bool {
	if version == nil || version.Meta.Key != "sub2api-official5" || version.Meta.Version != "1.1.0" {
		return false
	}
	source, err := builtin.Source("sub2api-official5")
	return err == nil && version.Source == source
}

func generationUploadReferences(task *GenerationTask) ([]generationUploadReference, error) {
	var body struct {
		Content []generationUploadReference `json:"content"`
	}
	if json.Unmarshal(task.Request, &body) != nil {
		return nil, errors.New("invalid upload reference request")
	}
	var result []generationUploadReference
	for _, item := range body.Content {
		if item.Ref == "" {
			continue
		}
		found := false
		for _, file := range task.Files {
			if file.Ref == item.Ref {
				kind := strings.TrimSuffix(item.Type, "_url")
				if (kind != "image" && kind != "video" && kind != "audio") || !strings.HasPrefix(file.MimeType, kind+"/") || math.IsNaN(item.Duration) || math.IsInf(item.Duration, 0) || item.Duration < 0 || item.Duration > 86400 || kind != "image" && item.Duration <= 0 {
					return nil, errors.New("upload reference does not match its bound media")
				}
				found = true
			}
		}
		if !found {
			return nil, errors.New("upload reference is not bound to this task")
		}
		result = append(result, item)
	}
	if len(result) > 4 {
		return nil, errors.New("too many bound upload references")
	}
	return result, nil
}

func (s *NewAPIPluginService) initializeTaskPreparation(ctx context.Context, task *GenerationTask) error {
	if len(task.Files) == 0 {
		return nil
	}
	version, err := s.repo.Get(ctx, task.PluginVersionID)
	if err != nil {
		return err
	}
	if !officialFiveUploadVersion(version) {
		return nil
	}
	refs, err := generationUploadReferences(task)
	if err != nil || len(refs) == 0 {
		return errors.New("official channel 5 files require bound upload references")
	}
	if s.preparations == nil || s.encryptor == nil || s.files == nil {
		return errors.New("bound upstream uploads are unavailable for this plugin revision")
	}
	task.PreparationStatus = "pending"
	return nil
}

func (s *NewAPIPluginService) loadTaskPreparation(task *GenerationTask) (*generationPreparation, error) {
	if s.encryptor == nil || task.PreparationEncrypted == "" || len(task.PreparationEncrypted) > 98304 {
		return nil, errors.New("upstream upload checkpoint is unavailable")
	}
	plain, err := s.encryptor.Decrypt(task.PreparationEncrypted)
	var value generationPreparation
	if err != nil || len(plain) > maxGenerationPreparation || json.Unmarshal([]byte(plain), &value) != nil || value.TaskID != task.ID || value.AccountID != task.AccountID || value.VersionID != task.PluginVersionID || value.Identity != generationPreparationIdentity(task) || value.APIKey == "" || len(value.Files) < 1 || len(value.Files) > 4 {
		return nil, errors.New("upstream upload checkpoint does not match this task")
	}
	return &value, nil
}

func (s *NewAPIPluginService) checkpointTaskPreparation(ctx context.Context, task *GenerationTask, value *generationPreparation) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxGenerationPreparation || s.encryptor == nil || s.preparations == nil {
		return errors.New("upstream upload checkpoint exceeds host limits")
	}
	sealed, err := s.encryptor.Encrypt(string(raw))
	if err != nil || len(sealed) > 98304 {
		return errors.New("cannot encrypt upstream upload checkpoint")
	}
	task.PreparationEncrypted = sealed
	return s.preparations.CheckpointPreparation(ctx, task)
}

func (s *NewAPIPluginService) officialUploadDo(account *Account, value *generationPreparation) officialupload.Do {
	return func(ctx context.Context, method, path string, headers map[string]string, body io.Reader, length int64) (json.RawMessage, error) {
		if s.gateway == nil || s.gateway.httpUpstream == nil || path != officialupload.Root && path != officialupload.Root+"/wait" && path != officialupload.Root+"/complete" && path != officialupload.Root+"/abort" && path != officialupload.Root+"/relay" {
			return nil, errors.New("reference-upload transport is unavailable")
		}
		base, err := s.gateway.validateUpstreamBaseURL(value.Base)
		if err != nil {
			return nil, err
		}
		url := strings.TrimRight(base, "/") + path
		if _, err = s.gateway.validateOutboundURL(url); err != nil {
			return nil, err
		}
		opCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(opCtx), method, url, body)
		if err != nil {
			return nil, err
		}
		req.ContentLength = length
		for key, val := range headers {
			req.Header.Set(key, val)
		}
		req.Header.Set("Authorization", "Bearer "+value.APIKey)
		proxy := ""
		if account.ProxyID != nil && account.Proxy != nil {
			proxy = account.Proxy.URL()
		}
		resp, err := s.gateway.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
		if err != nil {
			return nil, errors.New("reference-upload transport failed")
		}
		if resp == nil || resp.Body == nil {
			return nil, errors.New("reference-upload returned no response")
		}
		defer resp.Body.Close()
		stop := context.AfterFunc(opCtx, func() { _ = resp.Body.Close() })
		defer stop()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if resp.StatusCode >= 300 && resp.StatusCode < 500 && resp.StatusCode != 408 && resp.StatusCode != 409 && resp.StatusCode != 425 && resp.StatusCode != 429 {
				return nil, &officialupload.Permanent{Reason: fmt.Sprintf("reference-upload rejected with HTTP %d", resp.StatusCode)}
			}
			return nil, errors.New("reference-upload is temporarily unavailable")
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
		if err != nil || len(raw) > 64<<10 {
			return nil, errors.New("reference-upload response is unavailable or exceeds 64 KiB")
		}
		return raw, nil
	}
}

// Preparation never leases a JS worker and never sends a generation request.
func (s *NewAPIPluginService) prepareTaskFiles(ctx context.Context, account *Account, task *GenerationTask) error {
	version, err := s.repo.Get(ctx, task.PluginVersionID)
	if err != nil {
		return err
	}
	if !officialFiveUploadVersion(version) || task.PreparationStatus != "pending" || s.files == nil || s.preparations == nil {
		return &officialupload.Permanent{Reason: "upstream file preparation is unavailable"}
	}
	refs, err := generationUploadReferences(task)
	if err != nil || len(refs) == 0 {
		return &officialupload.Permanent{Reason: "invalid bound upload references"}
	}
	var value *generationPreparation
	if task.PreparationEncrypted == "" {
		value = &generationPreparation{TaskID: task.ID, AccountID: task.AccountID, VersionID: task.PluginVersionID, Identity: generationPreparationIdentity(task), Base: task.Connection.BaseURL, APIKey: account.GetCredential("api_key")}
		if value.APIKey == "" || account.Type != AccountTypeAPIKey {
			return &officialupload.Permanent{Reason: "upstream uploads require a configured API key"}
		}
		for _, ref := range refs {
			for _, file := range task.Files {
				if file.Ref == ref.Ref {
					identity := HashUsageRequestPayload([]byte(task.ID + "\x00" + ref.Ref + "\x00" + file.SHA256))
					value.Files = append(value.Files, generationUploadFile{Ref: ref.Ref, File: officialupload.File{SHA256: file.SHA256, Size: file.Size, Kind: strings.TrimSuffix(ref.Type, "_url"), ContentType: file.MimeType, IdempotencyKey: "sub2api-" + identity, DurationSeconds: ref.Duration}})
				}
			}
		}
		if err = s.checkpointTaskPreparation(ctx, task, value); err != nil {
			return err
		}
	} else {
		value, err = s.loadTaskPreparation(task)
		if err != nil {
			return &officialupload.Permanent{Reason: "invalid upstream upload checkpoint"}
		}
	}
	if value.APIKey != account.GetCredential("api_key") {
		return &officialupload.Permanent{Reason: "upstream upload credential changed"}
	}
	session := &newAPITaskSession{service: s, task: task}
	save := func(ctx context.Context) error { return s.checkpointTaskPreparation(ctx, task, value) }
	for i := range value.Files {
		file := &value.Files[i]
		if file.State.Reference != nil {
			if err = file.State.Reference.Validate(file.File); err != nil {
				return &officialupload.Permanent{Reason: "prepared reference changed"}
			}
			continue
		}
		// Verify every byte before sending any of this file to the upstream.
		// A spool also permits bounded multipart seeks after a worker restart.
		spool, err := newGenerationSpool()
		if err != nil {
			return err
		}
		err = func() error {
			defer spool.Close()
			_, reader, err := session.openFile(ctx, file.Ref)
			if err != nil {
				return &officialupload.Permanent{Reason: "bound reference file is unavailable"}
			}
			stop := context.AfterFunc(ctx, func() { _ = reader.Close() })
			defer stop()
			defer reader.Close()
			hasher := sha256.New()
			n, err := io.CopyBuffer(io.MultiWriter(spool, hasher), io.LimitReader(reader, file.File.Size+1), make([]byte, 32<<10))
			if err != nil || n != file.File.Size || hex.EncodeToString(hasher.Sum(nil)) != file.File.SHA256 {
				return &officialupload.Permanent{Reason: "bound reference file failed integrity checks"}
			}
			return officialupload.Ensure(ctx, file.File, spool, &file.State, s.officialUploadDo(account, value), save)
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *NewAPIPluginService) resolveTaskPreparedBody(account *Account, task *GenerationTask, body json.RawMessage) (json.RawMessage, error) {
	if task.PreparationStatus == "" {
		return body, nil
	}
	if task.PreparationStatus != "ready" {
		return nil, errors.New("upstream file preparation is incomplete")
	}
	value, err := s.loadTaskPreparation(task)
	if err != nil || value.APIKey != account.GetCredential("api_key") {
		return nil, errors.New("prepared upload no longer matches the upstream credential")
	}
	var fields map[string]json.RawMessage
	var items []map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || json.Unmarshal(fields["content"], &items) != nil {
		return nil, errors.New("invalid prepared upload body")
	}
	for _, item := range items {
		var ref string
		if raw, found := item["file_ref"]; found {
			if json.Unmarshal(raw, &ref) != nil || ref == "" {
				return nil, errors.New("invalid prepared file reference")
			}
			object := ""
			for _, file := range value.Files {
				if file.Ref == ref && file.State.Reference != nil && file.State.Reference.Validate(file.File) == nil {
					object = file.State.Reference.ObjectID
				}
			}
			if object == "" {
				return nil, errors.New("prepared file reference is unavailable")
			}
			delete(item, "file_ref")
			delete(item, "duration_seconds")
			item["storage_object_id"], _ = json.Marshal(object)
		}
	}
	fields["content"], err = json.Marshal(items)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func (s *NewAPIPluginService) cleanupTaskPreparation(ctx context.Context, account *Account, task *GenerationTask) error {
	if task.PreparationEncrypted == "" {
		return nil
	}
	value, err := s.loadTaskPreparation(task)
	if err != nil {
		return err
	}
	for i := range value.Files {
		if err = officialupload.Abort(ctx, &value.Files[i].State, s.officialUploadDo(account, value), func(ctx context.Context) error {
			return s.checkpointTaskPreparation(ctx, task, value)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *GenerationService) workPreparationCleanup(ctx context.Context, repo GenerationPreparationRepository) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		task, err := repo.ClaimPreparationCleanup(callCtx, uuid.NewString(), 2*time.Minute)
		if err == nil {
			account, accountErr := s.accounts.GetByID(callCtx, task.AccountID)
			if accountErr != nil {
				account = &Account{ID: task.AccountID, Concurrency: 1}
			}
			err = s.plugins.cleanupTaskPreparation(callCtx, account, task)
			if saveErr := repo.FinishPreparationCleanup(callCtx, task, err == nil, time.Minute); saveErr != nil {
				err = saveErr
			}
		}
		cancel()
		if err != nil && !errors.Is(err, ErrGenerationTaskNotFound) && !errors.Is(err, context.Canceled) {
			slog.Warn("generation_preparation_cleanup", "error_type", fmt.Sprintf("%T", err))
		}
	}
}
