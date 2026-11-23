package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
)

var _ service.GenerationFileRepository = (*generationTaskRepository)(nil)

const generationFileColumns = `id,filename,mime_type,size,sha256,created_at,expires_at`
const generationFileRetained = `(expires_at>NOW() OR EXISTS(SELECT 1 FROM sub2api_generation_task_files tf JOIN sub2api_generation_tasks t ON t.id=tf.task_id WHERE tf.file_id=f.id AND (t.billing_status<>'settled' OR t.finished_at IS NULL OR GREATEST(t.finished_at,t.updated_at)>NOW()-INTERVAL '7 days')))`

func scanGenerationFile(row interface{ Scan(...any) error }) (*service.GenerationFile, error) {
	f := &service.GenerationFile{Object: "video.file"}
	err := row.Scan(&f.ID, &f.Filename, &f.MimeType, &f.Size, &f.SHA256, &f.CreatedAt, &f.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGenerationFileNotFound
	}
	return f, err
}

func (r *generationTaskRepository) StoreGenerationFile(ctx context.Context, user, key, group int64, filename, kind string, source io.ReadSeeker) (*service.GenerationFile, error) {
	if user <= 0 || key <= 0 || group <= 0 || source == nil {
		return nil, errors.New("invalid file owner")
	}
	filename, kind, err := service.ValidateGenerationFilename(filename, kind)
	if err != nil {
		return nil, err
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(source, pluginTask.MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if size <= 0 || size > pluginTask.MaxFileBytes {
		return nil, errors.New("file size exceeds limit")
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	id, err := service.NewGenerationFileID()
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize capacity admission across nodes, only after the bounded input
	// is on host-owned disk. A slow uploading client never holds this lock.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('sub2api-generation-files-capacity'))`); err != nil {
		return nil, err
	}
	var total, owned int64
	var count, userCount int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0),COALESCE(SUM(size) FILTER(WHERE user_id=$1),0),COUNT(*),COUNT(*) FILTER(WHERE user_id=$1) FROM sub2api_generation_files`, user).Scan(&total, &owned, &count, &userCount); err != nil {
		return nil, err
	}
	if total+size > service.GenerationFileGlobalQuota || owned+size > service.GenerationFileUserQuota || count >= service.GenerationFileGlobalCount || userCount >= service.GenerationFileUserCount {
		return nil, service.ErrGenerationFileQuota
	}
	var owner bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_keys k JOIN users u ON u.id=k.user_id JOIN groups g ON g.id=k.group_id WHERE k.id=$1 AND k.user_id=$2 AND k.group_id=$3 AND k.status='active' AND k.deleted_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>NOW()) AND u.status='active' AND u.deleted_at IS NULL AND g.deleted_at IS NULL)`, key, user, group).Scan(&owner); err != nil {
		return nil, err
	}
	if !owner {
		return nil, service.ErrGenerationFileNotFound
	}
	f, err := scanGenerationFile(tx.QueryRowContext(ctx, `INSERT INTO sub2api_generation_files(id,user_id,api_key_id,group_id,filename,mime_type,size,sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+generationFileColumns, id, user, key, group, filename, kind, size, hex.EncodeToString(digest.Sum(nil))))
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, service.GenerationFileChunkBytes)
	var written int64
	check := sha256.New()
	for position := 0; written < size; position++ {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := io.ReadFull(source, buffer[:min(int64(len(buffer)), size-written)])
		if readErr != nil {
			return nil, readErr
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO sub2api_generation_file_chunks(file_id,position,content) VALUES($1,$2,$3)`, id, position, buffer[:n]); err != nil {
			return nil, err
		}
		_, _ = check.Write(buffer[:n])
		written += int64(n)
	}
	var extra [1]byte
	n, readErr := source.Read(extra[:])
	if n != 0 || readErr != io.EOF || hex.EncodeToString(check.Sum(nil)) != f.SHA256 {
		return nil, errors.New("file changed during persistence")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return f, nil
}

func (r *generationTaskRepository) GetGenerationFile(ctx context.Context, id string, user, key, group int64) (*service.GenerationFile, error) {
	return scanGenerationFile(r.db.QueryRowContext(ctx, `SELECT `+generationFileColumns+` FROM sub2api_generation_files f WHERE id=$1 AND user_id=$2 AND api_key_id=$3 AND group_id=$4 AND `+generationFileRetained, id, user, key, group))
}

func (r *generationTaskRepository) OpenGenerationFile(ctx context.Context, id string, user, key, group int64) (io.ReadCloser, error) {
	f, err := r.GetGenerationFile(ctx, id, user, key, group)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT position,content FROM sub2api_generation_file_chunks c JOIN sub2api_generation_files f ON f.id=c.file_id WHERE f.id=$1 AND f.user_id=$2 AND f.api_key_id=$3 AND f.group_id=$4 AND `+generationFileRetained+` ORDER BY position`, id, user, key, group)
	if err != nil {
		return nil, err
	}
	return &generationFileReader{rows: rows, digest: sha256.New(), size: f.Size, expected: f.SHA256}, nil
}

type generationFileReader struct {
	rows       *sql.Rows
	buffer     []byte
	digest     hash.Hash
	size, read int64
	position   int
	expected   string
	done       bool
}

func (r *generationFileReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if r.done {
		return 0, io.EOF
	}
	if len(r.buffer) == 0 {
		if !r.rows.Next() {
			err := r.rows.Err()
			_ = r.rows.Close()
			r.done = true
			if err != nil {
				return 0, err
			}
			if r.read != r.size || hex.EncodeToString(r.digest.Sum(nil)) != r.expected {
				return 0, errors.New("stored file integrity check failed")
			}
			return 0, io.EOF
		}
		var position int
		if err := r.rows.Scan(&position, &r.buffer); err != nil {
			_ = r.Close()
			return 0, err
		}
		if position != r.position || len(r.buffer) == 0 || len(r.buffer) > service.GenerationFileChunkBytes || r.read+int64(len(r.buffer)) > r.size {
			_ = r.Close()
			return 0, errors.New("invalid stored file chunk")
		}
		r.position++
		_, _ = r.digest.Write(r.buffer)
		r.read += int64(len(r.buffer))
	}
	n := copy(dst, r.buffer)
	r.buffer = r.buffer[n:]
	return n, nil
}
func (r *generationFileReader) Close() error { r.done = true; r.buffer = nil; return r.rows.Close() }

func (r *generationTaskRepository) DeleteGenerationFile(ctx context.Context, id string, user, key, group int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var found string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM sub2api_generation_files WHERE id=$1 AND user_id=$2 AND api_key_id=$3 AND group_id=$4 FOR UPDATE`, id, user, key, group).Scan(&found); errors.Is(err, sql.ErrNoRows) {
		return service.ErrGenerationFileNotFound
	} else if err != nil {
		return err
	}
	var bound bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sub2api_generation_task_files WHERE file_id=$1)`, id).Scan(&bound); err != nil {
		return err
	}
	if bound {
		return service.ErrGenerationFileInUse
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sub2api_generation_files WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *generationTaskRepository) CleanupGenerationFiles(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 100 {
		return 0, errors.New("invalid file cleanup limit")
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM sub2api_generation_files WHERE id IN(SELECT f.id FROM sub2api_generation_files f WHERE NOT `+generationFileRetained+` ORDER BY expires_at,id LIMIT $1 FOR UPDATE SKIP LOCKED)`, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func bindGenerationFiles(ctx context.Context, tx *sql.Tx, t *service.GenerationTask) error {
	files := append([]service.GenerationFileSnapshot(nil), t.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].ID < files[j].ID })
	seen := map[string]*service.GenerationFile{}
	for _, snapshot := range files {
		f, bound := seen[snapshot.ID]
		if !bound {
			var err error
			f, err = scanGenerationFile(tx.QueryRowContext(ctx, `SELECT `+generationFileColumns+` FROM sub2api_generation_files f WHERE id=$1 AND user_id=$2 AND api_key_id=$3 AND group_id=$4 AND `+generationFileRetained+` FOR UPDATE`, snapshot.ID, t.UserID, t.APIKeyID, t.GroupID))
			if err != nil {
				return err
			}
		}
		if f.Size != snapshot.Size || f.SHA256 != snapshot.SHA256 || f.Filename != snapshot.Filename || f.MimeType != snapshot.MimeType {
			return errors.New("task file differs from stored snapshot")
		}
		// A single stored image may be used for two protocol roles. Retain one
		// SQL association, while validating every opaque ref's metadata.
		if bound {
			continue
		}
		seen[snapshot.ID] = f
		if _, err := tx.ExecContext(ctx, `INSERT INTO sub2api_generation_task_files(task_id,file_id) VALUES($1,$2)`, t.ID, snapshot.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sub2api_generation_files SET expires_at=GREATEST(expires_at,NOW()+INTERVAL '7 days') WHERE id=$1`, snapshot.ID); err != nil {
			return err
		}
	}
	return nil
}

// Keep this shape bounded before persisting it or handing it to JavaScript.
func generationFileSnapshotJSON(files []service.GenerationFileSnapshot) ([]byte, error) {
	if files == nil {
		files = []service.GenerationFileSnapshot{}
	}
	data, err := json.Marshal(files)
	if err != nil || len(files) > pluginTask.MaxTaskFiles || len(data) > 16384 {
		return nil, errors.New("invalid file snapshot")
	}
	return data, nil
}
