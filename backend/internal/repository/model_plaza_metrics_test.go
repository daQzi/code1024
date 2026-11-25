package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestModelPlazaMetricsScopesAndNullableValues(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer db.Close()
	repo := NewModelPlazaMetricsRepository(db)
	end := time.Now().UTC()
	start := end.Add(-24 * time.Hour)
	columns := []string{"group", "bucket", "requests", "successes", "avg", "p95", "ttft", "tps"}
	mock.ExpectQuery(plazaMetricsQuery).WithArgs(start, end, sqlmock.AnyArg()).WillReturnRows(
		sqlmock.NewRows(columns).AddRow(0, nil, 0, 0, nil, nil, nil, nil).AddRow(4, end.Truncate(time.Hour), 5, 4, 2000, 3000, 100, 20))
	points, err := repo.ReadModelMetrics(context.Background(), "vendor/model", "openai", []int64{4}, start, end)
	require.NoError(t, err)
	require.Len(t, points, 2)
	require.Nil(t, points[0].AverageMs)
	require.Nil(t, points[0].Bucket)
	require.NotNil(t, points[1].Bucket)
	require.Equal(t, float64(2000), *points[1].AverageMs)
	_, err = repo.ReadModelMetrics(context.Background(), "vendor/model", "openai", []int64{4}, start.Add(-time.Minute), end)
	require.Error(t, err)
	points, err = repo.ReadModelMetrics(context.Background(), "vendor/model", "openai", nil, start, end)
	require.NoError(t, err)
	require.Empty(t, points)

	// Sub-hour 90m window uses 5m (300s) bucket interval as 4th query argument.
	start90 := end.Add(-90 * time.Minute)
	mock.ExpectQuery(plazaMetricsSubHourQuery).WithArgs(start90, end, sqlmock.AnyArg(), float64(300)).WillReturnRows(
		sqlmock.NewRows(columns).AddRow(0, nil, 10, 9, 1200, 1800, 80, 40))
	subPoints, err := repo.ReadModelMetrics(context.Background(), "vendor/model", "openai", []int64{4}, start90, end)
	require.NoError(t, err)
	require.Len(t, subPoints, 1)
	require.Equal(t, int64(9), subPoints[0].Successes)

	require.NoError(t, mock.ExpectationsWereMet())
}
