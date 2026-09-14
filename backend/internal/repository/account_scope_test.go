package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOrdinaryAccountPlatforms_RemovesHuggingFaceAndDuplicates(t *testing.T) {
	platforms := ordinaryAccountPlatforms([]string{
		service.PlatformOpenAI,
		" " + service.PlatformHuggingFace + " ",
		service.PlatformAnthropic,
		service.PlatformOpenAI,
		" ",
	})

	require.Equal(t, []string{service.PlatformOpenAI, service.PlatformAnthropic}, platforms)
	require.False(t, isOrdinaryAccountPlatform(service.PlatformHuggingFace))
	require.False(t, isOrdinaryAccountPlatform(" "+service.PlatformHuggingFace+" "))
	require.True(t, isOrdinaryAccountPlatform(service.PlatformOpenAI))
}

func TestListSchedulableCapacityByGroupIDs_ExcludesHuggingFace(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var capturedSQL string
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{
			"group_id",
			"account_id",
			"concurrency",
			"extra",
			"session_window_start",
			"session_window_end",
			"session_window_status",
		}))
	repo := newAccountRepositoryWithSQL(nil, captureQuerySQL{db: db, captured: &capturedSQL}, nil)

	rows, err := repo.ListSchedulableCapacityByGroupIDs(context.Background(), []int64{7, 7})
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Contains(t, normalizeSQLWhitespace(capturedSQL), ordinaryAccountAliasedSQLPredicate)
	require.NoError(t, mock.ExpectationsWereMet())
}
