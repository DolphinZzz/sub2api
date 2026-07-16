package repository

import (
	"context"
	"fmt"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestIsPostgresDatabaseMissing(t *testing.T) {
	require.True(t, isPostgresDatabaseMissing(fmt.Errorf("wrapped: %w", &pq.Error{Code: "3D000"})))
	require.False(t, isPostgresDatabaseMissing(fmt.Errorf("wrapped: %w", &pq.Error{Code: "28000"})))
	require.False(t, isPostgresDatabaseMissing(nil))
}

func TestEnsurePostgresDatabaseExistsRejectsUnsafeName(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	err = ensurePostgresDatabaseExists(context.Background(), db, "sub2api;DROP")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid database name")
}

func TestEnsurePostgresDatabaseExistsSkipsExistingDatabase(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("sub2api").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	require.NoError(t, ensurePostgresDatabaseExists(context.Background(), db, "sub2api"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsurePostgresDatabaseExistsCreatesMissingDatabase(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("sub2api").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec("CREATE DATABASE sub2api").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, ensurePostgresDatabaseExists(context.Background(), db, "sub2api"))
	require.NoError(t, mock.ExpectationsWereMet())
}
