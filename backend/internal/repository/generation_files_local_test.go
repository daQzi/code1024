package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type generationFileSQLFixture struct {
	db                                 *sql.DB
	repo                               *generationTaskRepository
	user, key, group, account, version int64
}

func newGenerationFileSQLFixture(t *testing.T) *generationFileSQLFixture {
	t.Helper()
	db := generationLocalDB(t)
	f := &generationFileSQLFixture{db: db, repo: &generationTaskRepository{db: db}}
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance,concurrency) VALUES('files@example.invalid','fixture',100,10) RETURNING id`).Scan(&f.user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('files','openai') RETURNING id`).Scan(&f.group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'file-key','file-key',$2) RETURNING id`, f.user, f.group).Scan(&f.key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('files','openai','apikey','{}',10,1) RETURNING id`).Scan(&f.account))
	source, err := builtin.Source("sora")
	require.NoError(t, err)
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	version, err := NewNewAPIPluginRepository(db).Install(context.Background(), &service.NewAPIPluginVersion{Key: loaded.Meta.Key, Version: loaded.Meta.Version, SHA256: strings.Repeat("a", 64), Source: source, Meta: loaded.Meta, Capabilities: runner.Descriptor{Task: true}}, nil)
	require.NoError(t, err)
	f.version = version.ID
	return f
}
func (f *generationFileSQLFixture) task(t *testing.T, file *service.GenerationFile) (*service.GenerationTask, *service.UsageBillingCommand) {
	t.Helper()
	id, err := service.NewGenerationTaskID()
	require.NoError(t, err)
	unit := decimal.NewFromInt(1)
	price, err := service.NewGenerationPriceSnapshot(service.GenerationPriceRule{Mode: "fixed", Unit: "request", UnitPrice: &unit}, unit, nil, unit)
	require.NoError(t, err)
	task := &service.GenerationTask{ID: id, UserID: f.user, APIKeyID: f.key, GroupID: f.group, AccountID: f.account, PluginVersionID: f.version, IdempotencyKey: id, Fingerprint: strings.Repeat("b", 64), Model: "sora-2", UpstreamModel: "sora-2", Platform: "openai", Protocol: "openai_video", Action: "image_to_video", Request: json.RawMessage(`{"prompt":"fixture"}`), Price: price, Connection: service.GenerationConnection{BaseURL: "https://fixture.example.invalid", Kind: "vendor"}}
	task.Files = []service.GenerationFileSnapshot{{ID: file.ID, SHA256: file.SHA256, File: pluginTask.File{Ref: "request_file:input_reference", Field: "input_reference", Filename: file.Filename, MimeType: file.MimeType, Size: file.Size}}}
	return task, &service.UsageBillingCommand{UserID: f.user, APIKeyID: f.key, AccountID: f.account, AccountType: service.AccountTypeAPIKey, BalanceCost: 1, APIKeyQuotaCost: 1, APIKeyRateLimitCost: 1}
}

func TestGenerationFilesLocalPrivateChunksAndAtomicTaskBinding(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	content := bytes.Repeat([]byte("a private reference "), 10000)
	file, err := f.repo.StoreGenerationFile(ctx, f.user, f.key, f.group, "input.png", "image/png", bytes.NewReader(content))
	require.NoError(t, err)
	require.EqualValues(t, len(content), file.Size)
	for _, owner := range [][3]int64{{f.user + 1, f.key, f.group}, {f.user, f.key + 1, f.group}, {f.user, f.key, f.group + 1}} {
		_, err := f.repo.GetGenerationFile(ctx, file.ID, owner[0], owner[1], owner[2])
		require.ErrorIs(t, err, service.ErrGenerationFileNotFound)
		_, err = f.repo.OpenGenerationFile(ctx, file.ID, owner[0], owner[1], owner[2])
		require.ErrorIs(t, err, service.ErrGenerationFileNotFound)
		err = f.repo.DeleteGenerationFile(ctx, file.ID, owner[0], owner[1], owner[2])
		require.ErrorIs(t, err, service.ErrGenerationFileNotFound)
	}
	reader, err := f.repo.OpenGenerationFile(ctx, file.ID, f.user, f.key, f.group)
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, content, got)
	// A fresh repository instance can read the same durable input.
	restarted := &generationTaskRepository{db: f.db}
	_, err = restarted.GetGenerationFile(ctx, file.ID, f.user, f.key, f.group)
	require.NoError(t, err)
	task, command := f.task(t, file)
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.repo.CreateHeld(ctx, task, command); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	stored, err := restarted.GetOwned(ctx, task.ID, f.user, f.key)
	require.NoError(t, err)
	require.Equal(t, task.Files, stored.Files)
	var count int
	var balance float64
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_task_files WHERE task_id=$1`, task.ID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
	require.Equal(t, 99.0, balance)
	require.ErrorIs(t, f.repo.DeleteGenerationFile(ctx, file.ID, f.user, f.key, f.group), service.ErrGenerationFileInUse)
	// Expiration never removes inputs needed by an unfinished/review task.
	_, err = f.db.Exec(`UPDATE sub2api_generation_files SET expires_at=NOW()-INTERVAL '1 day' WHERE id=$1`, file.ID)
	require.NoError(t, err)
	n, err := f.repo.CleanupGenerationFiles(ctx, 20)
	require.NoError(t, err)
	require.Zero(t, n)
	_, err = f.repo.GetGenerationFile(ctx, file.ID, f.user, f.key, f.group)
	require.NoError(t, err)
	// Retention is anchored to final settlement and completion, not upload time.
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET billing_status='settled',status='succeeded',finished_at=NOW()-INTERVAL '8 days',updated_at=NOW()-INTERVAL '8 days' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	n, err = f.repo.CleanupGenerationFiles(ctx, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_file_chunks WHERE file_id=$1`, file.ID).Scan(&count))
	require.Zero(t, count)
	_, err = f.repo.GetOwned(ctx, task.ID, f.user, f.key)
	require.NoError(t, err) // task/financial facts survive file cleanup
}

func TestGenerationFilesLocalMultipleRolesBindOnceAndRollbackTogether(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	first, err := f.repo.StoreGenerationFile(ctx, f.user, f.key, f.group, "first.png", "image/png", strings.NewReader("first"))
	require.NoError(t, err)
	last, err := f.repo.StoreGenerationFile(ctx, f.user, f.key, f.group, "last.png", "image/png", strings.NewReader("last"))
	require.NoError(t, err)
	task, command := f.task(t, first)
	task.Files = append(task.Files, service.GenerationFileSnapshot{ID: last.ID, SHA256: last.SHA256, File: pluginTask.File{Ref: "request_file:image[]", Field: "image[]", Filename: last.Filename, MimeType: last.MimeType, Size: last.Size}})
	reuse := task.Files[0]
	reuse.Ref = "request_file:input_reference:1"
	task.Files = append(task.Files, reuse)
	require.NoError(t, task.Validate())
	_, err = f.repo.CreateHeld(ctx, task, command)
	require.NoError(t, err)
	stored, err := (&generationTaskRepository{db: f.db}).GetOwned(ctx, task.ID, f.user, f.key)
	require.NoError(t, err)
	require.Equal(t, task.Files, stored.Files)
	var count int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM sub2api_generation_task_files WHERE task_id=$1`, task.ID).Scan(&count))
	require.Equal(t, 2, count, "repeated metadata roles retain one physical file binding")
	for _, file := range []*service.GenerationFile{first, last} {
		require.ErrorIs(t, f.repo.DeleteGenerationFile(ctx, file.ID, f.user, f.key, f.group), service.ErrGenerationFileInUse)
	}
	// Validate the reused role as well, so a duplicate ID cannot bypass the
	// immutable metadata comparison after its first binding.
	invalid, command := f.task(t, first)
	bad := invalid.Files[0]
	bad.Ref = "request_file:input_reference:1"
	bad.Size++
	invalid.Files = append(invalid.Files, bad)
	_, err = f.repo.CreateHeld(ctx, invalid, command)
	require.ErrorContains(t, err, "differs")
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM sub2api_generation_task_files WHERE task_id=$1`, invalid.ID).Scan(&count))
	require.Zero(t, count)
	var balance float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
	require.Equal(t, 99.0, balance)
}

func TestGenerationFilesLocalExpiryQuotaCorruptionAndRollback(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	file, err := f.repo.StoreGenerationFile(ctx, f.user, f.key, f.group, "input.png", "image/png", strings.NewReader("abc"))
	require.NoError(t, err)
	// Chunk-integrity failures must never be presented as a successful file.
	_, err = f.db.Exec(`UPDATE sub2api_generation_file_chunks SET content='bad'::bytea WHERE file_id=$1`, file.ID)
	require.NoError(t, err)
	reader, err := f.repo.OpenGenerationFile(ctx, file.ID, f.user, f.key, f.group)
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.ErrorContains(t, err, "integrity")
	require.NoError(t, reader.Close())
	require.NoError(t, f.repo.DeleteGenerationFile(ctx, file.ID, f.user, f.key, f.group))
	file, err = f.repo.StoreGenerationFile(ctx, f.user, f.key, f.group, "input.png", "image/png", strings.NewReader("abc"))
	require.NoError(t, err)
	task, command := f.task(t, file)
	command.BalanceCost = 200 // invalid funding rolls back the task and file association
	_, err = f.repo.CreateHeld(ctx, task, command)
	require.Error(t, err)
	var count int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_task_files`).Scan(&count))
	require.Zero(t, count)
	// A failure after file binding (insufficient balance in the same SQL
	// transaction) must also release the file association and the reservation.
	_, err = f.db.Exec(`UPDATE users SET balance=0 WHERE id=$1`, f.user)
	require.NoError(t, err)
	task, command = f.task(t, file)
	_, err = f.repo.CreateHeld(ctx, task, command)
	require.ErrorIs(t, err, service.ErrBatchImageInsufficientBalance)
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_task_files`).Scan(&count))
	require.Zero(t, count)
	_, err = f.db.Exec(`UPDATE users SET balance=100 WHERE id=$1`, f.user)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE sub2api_generation_files SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, file.ID)
	require.NoError(t, err)
	task, command = f.task(t, file)
	_, err = f.repo.CreateHeld(ctx, task, command)
	require.ErrorIs(t, err, service.ErrGenerationFileNotFound)
	var balance float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
	require.Equal(t, 100.0, balance)
	n, err := f.repo.CleanupGenerationFiles(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	// Fill only quota metadata, so the bound itself is tested without allocating
	// 128 MiB. Concurrent upload admission uses the same SQL serialization lock.
	for range 4 {
		id, err := service.NewGenerationFileID()
		require.NoError(t, err)
		_, err = f.db.Exec(`INSERT INTO sub2api_generation_files(id,user_id,api_key_id,group_id,filename,mime_type,size,sha256) VALUES($1,$2,$3,$4,'fixture','video/mp4',33554432,$5)`, id, f.user, f.key, f.group, strings.Repeat("a", 64))
		require.NoError(t, err)
	}
	_, err = f.repo.StoreGenerationFile(ctx, f.user, f.key, f.group, "input.png", "image/png", strings.NewReader("abc"))
	require.ErrorIs(t, err, service.ErrGenerationFileQuota)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = f.repo.StoreGenerationFile(canceled, f.user, f.key, f.group, "input.png", "image/png", strings.NewReader("abc"))
	require.ErrorIs(t, err, context.Canceled)
	// The new migration remains restart-safe.
	require.NoError(t, ApplyMigrations(ctx, f.db))
}
