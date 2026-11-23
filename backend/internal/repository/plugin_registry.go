package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const pluginSubmissionColumns = `id,author_id,plugin_key,version,sha256,source,readme,meta,capabilities,status,revision,review_reason,created_at,updated_at,published_at`

func scanPluginSubmission(row interface{ Scan(...any) error }) (*service.PluginSubmission, error) {
	item := &service.PluginSubmission{}
	var meta, capabilities []byte
	if err := row.Scan(&item.ID, &item.AuthorID, &item.Key, &item.Version, &item.SHA256, &item.Source, &item.Readme, &meta, &capabilities, &item.Status, &item.Revision, &item.ReviewReason, &item.CreatedAt, &item.UpdatedAt, &item.PublishedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrPluginSubmissionMissing
		}
		return nil, err
	}
	if err := json.Unmarshal(meta, &item.Meta); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(capabilities, &item.Capabilities); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *newAPIPluginRepository) ListPluginSubmissions(ctx context.Context, f service.PluginRegistryFilter) ([]service.PluginSubmission, int64, error) {
	if f.Page < 1 || f.Page > 100000 || f.PageSize < 1 || f.PageSize > 100 || f.Owner < 0 {
		return nil, 0, errors.New("invalid plugin registry pagination")
	}
	where := ` FROM sub2api_plugin_submissions WHERE ($1::bigint=0 OR author_id=$1) AND ($2::text='' OR status=$2)`
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*)`+where, f.Owner, f.Status).Scan(&total); err != nil {
		return nil, 0, err
	}
	// Source code and readmes are fetched only from the permission-checked detail endpoint.
	columns := `id,author_id,plugin_key,version,sha256,''::text,''::text,meta,capabilities,status,revision,review_reason,created_at,updated_at,published_at`
	rows, err := r.db.QueryContext(ctx, `SELECT `+columns+where+` ORDER BY updated_at DESC,id DESC LIMIT $3 OFFSET $4`, f.Owner, f.Status, f.PageSize, (f.Page-1)*f.PageSize)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.PluginSubmission, 0)
	for rows.Next() {
		item, err := scanPluginSubmission(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	return items, total, rows.Err()
}

func (r *newAPIPluginRepository) GetPluginSubmission(ctx context.Context, id int64) (*service.PluginSubmission, error) {
	return scanPluginSubmission(r.db.QueryRowContext(ctx, `SELECT `+pluginSubmissionColumns+` FROM sub2api_plugin_submissions WHERE id=$1`, id))
}

func registryAudit(ctx context.Context, tx *sql.Tx, id, actor, revision int64, action, hash, reason string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_submission_events(submission_id,actor_id,revision,action,sha256,reason) VALUES($1,$2,$3,$4,$5,$6)`, id, actor, revision, action, hash, reason)
	return err
}

func (r *newAPIPluginRepository) SavePluginDraft(ctx context.Context, item *service.PluginSubmission) (*service.PluginSubmission, error) {
	meta, err := json.Marshal(item.Meta)
	if err != nil {
		return nil, err
	}
	capabilities, err := json.Marshal(item.Capabilities)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('plugin-author:'||$1::text,0))`, item.AuthorID); err != nil {
		return nil, err
	}
	var id, revision int64
	if item.ID == 0 {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sub2api_plugin_submissions WHERE author_id=$1`, item.AuthorID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 64 {
			return nil, errors.New("每位作者最多保留 64 个投稿版本")
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_submissions(author_id,plugin_key,version,sha256,source,readme,meta,capabilities) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb) ON CONFLICT(plugin_key,version) DO NOTHING RETURNING id,revision`, item.AuthorID, item.Key, item.Version, item.SHA256, item.Source, item.Readme, meta, capabilities).Scan(&id, &revision)
	} else {
		err = tx.QueryRowContext(ctx, `UPDATE sub2api_plugin_submissions SET plugin_key=$4,version=$5,sha256=$6,source=$7,readme=$8,meta=$9::jsonb,capabilities=$10::jsonb,status='draft',review_reason='',reviewed_by=NULL,revision=revision+1,updated_at=NOW() WHERE id=$1 AND author_id=$2 AND revision=$3 AND status IN('draft','rejected') RETURNING id,revision`, item.ID, item.AuthorID, item.Revision, item.Key, item.Version, item.SHA256, item.Source, item.Readme, meta, capabilities).Scan(&id, &revision)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrPluginSubmissionConflict
	}
	if err != nil {
		return nil, err
	}
	if err = registryAudit(ctx, tx, id, item.AuthorID, revision, "save_draft", item.SHA256, ""); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetPluginSubmission(ctx, id)
}

func (r *newAPIPluginRepository) TransitionPluginSubmission(ctx context.Context, id, actor, revision int64, action, reason string, admin bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := scanPluginSubmission(tx.QueryRowContext(ctx, `SELECT `+pluginSubmissionColumns+` FROM sub2api_plugin_submissions WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if !admin && item.AuthorID != actor {
		return service.ErrPluginSubmissionMissing
	}
	if item.Revision != revision {
		return service.ErrPluginSubmissionConflict
	}
	state := ""
	switch action {
	case "submit":
		if item.AuthorID == actor && (item.Status == "draft" || item.Status == "rejected") {
			state = "submitted"
		}
	case "withdraw":
		if item.Status == "submitted" || item.Status == "published" {
			state = "withdrawn"
		}
	case "publish":
		if admin && item.Status == "submitted" {
			state = "published"
		}
	case "reject":
		if admin && item.Status == "submitted" {
			state = "rejected"
		}
	}
	if state == "" {
		return service.ErrPluginSubmissionConflict
	}
	// Reviewer identity is host-owned; authors can never manufacture approval.
	_, err = tx.ExecContext(ctx, `UPDATE sub2api_plugin_submissions SET status=$2::text,revision=revision+1,review_reason=$3,reviewed_by=CASE WHEN $4::boolean THEN $5::bigint ELSE reviewed_by END,published_at=CASE WHEN $2::text='published' THEN NOW() ELSE published_at END,updated_at=NOW() WHERE id=$1`, id, state, reason, admin && (action == "publish" || action == "reject"), actor)
	if err != nil {
		return err
	}
	if err = registryAudit(ctx, tx, id, actor, revision+1, action, item.SHA256, reason); err != nil {
		return fmt.Errorf("registry audit: %w", err)
	}
	return tx.Commit()
}
