package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type newAPIPluginRepository struct{ db *sql.DB }

func NewNewAPIPluginRepository(db *sql.DB) service.NewAPIPluginRepository {
	return &newAPIPluginRepository{db: db}
}

const newAPIPluginSelect = `SELECT v.id,v.plugin_key,v.version,v.sha256,v.origin,v.source,v.meta,v.capabilities,
 p.enabled,(p.active_version_id=v.id),v.created_at
 FROM sub2api_js_plugin_versions v JOIN sub2api_js_plugins p USING(plugin_key)`

func scanNewAPIPlugin(row interface{ Scan(...any) error }) (*service.NewAPIPluginVersion, error) {
	v := &service.NewAPIPluginVersion{}
	var meta, capabilities []byte
	if err := row.Scan(&v.ID, &v.Key, &v.Version, &v.SHA256, &v.Origin, &v.Source, &meta, &capabilities, &v.Enabled, &v.Active, &v.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(meta, &v.Meta); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(capabilities, &v.Capabilities); err != nil {
		return nil, err
	}
	return v, nil
}

func (r *newAPIPluginRepository) query(ctx context.Context, query string, args ...any) ([]service.NewAPIPluginVersion, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.NewAPIPluginVersion, 0)
	for rows.Next() {
		v, err := scanNewAPIPlugin(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *v)
	}
	return result, rows.Err()
}

func (r *newAPIPluginRepository) List(ctx context.Context) ([]service.NewAPIPluginVersion, error) {
	return r.query(ctx, strings.Replace(newAPIPluginSelect, "v.source", "''::text", 1)+` WHERE p.active_version_id=v.id ORDER BY v.plugin_key LIMIT 256`)
}
func (r *newAPIPluginRepository) Versions(ctx context.Context, key string) ([]service.NewAPIPluginVersion, error) {
	return r.query(ctx, strings.Replace(newAPIPluginSelect, "v.source", "''::text", 1)+` WHERE v.plugin_key=$1 ORDER BY v.created_at DESC,v.id DESC LIMIT 64`, key)
}
func (r *newAPIPluginRepository) Get(ctx context.Context, id int64) (*service.NewAPIPluginVersion, error) {
	return scanNewAPIPlugin(r.db.QueryRowContext(ctx, newAPIPluginSelect+` WHERE v.id=$1`, id))
}
func (r *newAPIPluginRepository) Install(ctx context.Context, v *service.NewAPIPluginVersion, actor *int64) (*service.NewAPIPluginVersion, error) {
	meta, err := json.Marshal(v.Meta)
	if err != nil {
		return nil, err
	}
	capabilities, err := json.Marshal(v.Capabilities)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Bound retained source storage and serialize installs across replicas.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(734562910245)`); err != nil {
		return nil, err
	}
	if strings.HasPrefix(v.Origin, "community:") {
		id, parseErr := strconv.ParseInt(strings.TrimPrefix(v.Origin, "community:"), 10, 64)
		if parseErr != nil || id <= 0 {
			return nil, service.ErrPluginSubmissionConflict
		}
		var status, hash, key, version string
		// Serialize withdrawal with installation: withdrawn source cannot be
		// admitted after the publication check and before this commit.
		if err = tx.QueryRowContext(ctx, `SELECT status,sha256,plugin_key,version FROM sub2api_plugin_submissions WHERE id=$1 FOR SHARE`, id).Scan(&status, &hash, &key, &version); err != nil {
			return nil, service.ErrPluginSubmissionMissing
		}
		if status != "published" || hash != v.SHA256 || key != v.Key || version != v.Version {
			return nil, service.ErrPluginSubmissionConflict
		}
	}
	var existingID int64
	var existingHash string
	err = tx.QueryRowContext(ctx, `SELECT id,sha256 FROM sub2api_js_plugin_versions WHERE plugin_key=$1 AND (version=$2 OR sha256=$3) ORDER BY (version=$2) DESC LIMIT 1`, v.Key, v.Version, v.SHA256).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != v.SHA256 {
			return nil, service.ErrPluginVersionConflict
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return r.Get(ctx, existingID)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	var keyCount, versionCount int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM sub2api_js_plugins),(SELECT count(*) FROM sub2api_js_plugin_versions WHERE plugin_key=$1)`, v.Key).Scan(&keyCount, &versionCount); err != nil {
		return nil, err
	}
	if versionCount >= 64 || versionCount == 0 && keyCount >= 256 {
		return nil, errors.New("插件或历史版本数量达到上限")
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO sub2api_js_plugin_versions(plugin_key,version,sha256,origin,source,meta,capabilities,installed_by)
 VALUES($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8) RETURNING id`, v.Key, v.Version, v.SHA256, v.Origin, v.Source, meta, capabilities, actor).Scan(&id)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sub2api_js_plugins(plugin_key,active_version_id) VALUES($1,$2) ON CONFLICT(plugin_key) DO NOTHING`, v.Key, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}
func (r *newAPIPluginRepository) Activate(ctx context.Context, id int64, enabled bool) error {
	result, err := r.db.ExecContext(ctx, `UPDATE sub2api_js_plugins p SET active_version_id=CASE WHEN $2 THEN v.id ELSE p.active_version_id END, enabled=$2,updated_at=NOW()
 FROM sub2api_js_plugin_versions v WHERE v.id=$1 AND p.plugin_key=v.plugin_key`, id, enabled)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return sql.ErrNoRows
	}
	return err
}

func (r *newAPIPluginRepository) SelectVersion(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `UPDATE sub2api_js_plugins p SET active_version_id=v.id,updated_at=NOW() FROM sub2api_js_plugin_versions v WHERE v.id=$1 AND p.plugin_key=v.plugin_key`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.ErrPluginStateChanged
	}
	return err
}
