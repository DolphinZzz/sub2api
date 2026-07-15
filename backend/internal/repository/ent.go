// Package repository 提供应用程序的基础设施层组件。
// 包括数据库连接初始化、ORM 客户端管理、Redis 连接、数据库迁移等核心功能。
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/migrations"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/lib/pq"
)

// InitEnt 初始化 Ent ORM 客户端并返回客户端实例和底层的 *sql.DB。
//
// 该函数执行以下操作：
//  1. 初始化全局时区设置，确保时间处理一致性
//  2. 建立 PostgreSQL 数据库连接
//  3. 自动执行数据库迁移，确保 schema 与代码同步
//  4. 创建并返回 Ent 客户端实例
//
// 重要提示：调用者必须负责关闭返回的 ent.Client（关闭时会自动关闭底层的 driver/db）。
//
// 参数：
//   - cfg: 应用程序配置，包含数据库连接信息和时区设置
//
// 返回：
//   - *ent.Client: Ent ORM 客户端，用于执行数据库操作
//   - *sql.DB: 底层的 SQL 数据库连接，可用于直接执行原生 SQL
//   - error: 初始化过程中的错误
func InitEnt(cfg *config.Config) (*ent.Client, *sql.DB, error) {
	// 优先初始化时区设置，确保所有时间操作使用统一的时区。
	// 这对于跨时区部署和日志时间戳的一致性至关重要。
	if err := timezone.Init(cfg.Timezone); err != nil {
		return nil, nil, err
	}

	// 构建包含时区信息的数据库连接字符串 (DSN)。
	// 时区信息会传递给 PostgreSQL，确保数据库层面的时间处理正确。
	dsn := cfg.Database.DSNWithTimezone(cfg.Timezone)

	// 使用 Ent 的 SQL 驱动打开 PostgreSQL 连接。
	// dialect.Postgres 指定使用 PostgreSQL 方言进行 SQL 生成。
	drv, err := openPostgresEntDriver(cfg, dsn)
	if err != nil {
		return nil, nil, err
	}
	applyDBPoolSettings(drv.DB(), cfg)

	// 确保数据库 schema 已准备就绪。
	// SQL 迁移文件是 schema 的权威来源（source of truth）。
	// 这种方式比 Ent 的自动迁移更可控，支持复杂的迁移场景。
	migrationCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := applyMigrationsFS(migrationCtx, drv.DB(), migrations.FS); err != nil {
		if isPostgresDatabaseMissing(err) {
			_ = drv.Close()
			if ensureErr := ensurePostgresDatabase(migrationCtx, cfg.Database, cfg.Timezone); ensureErr != nil {
				return nil, nil, fmt.Errorf("ensure database %q exists: %w", cfg.Database.DBName, ensureErr)
			}
			drv, err = openPostgresEntDriver(cfg, dsn)
			if err != nil {
				return nil, nil, err
			}
			applyDBPoolSettings(drv.DB(), cfg)
			if err = applyMigrationsFS(migrationCtx, drv.DB(), migrations.FS); err == nil {
				goto migrationsApplied
			}
		}
		_ = drv.Close() // 迁移失败时关闭驱动，避免资源泄露
		return nil, nil, err
	}

migrationsApplied:
	// 创建 Ent 客户端，绑定到已配置的数据库驱动。
	client := ent.NewClient(ent.Driver(drv))

	// 启动阶段：从配置或数据库中确保系统密钥可用。
	if err := ensureBootstrapSecrets(migrationCtx, client, cfg); err != nil {
		_ = client.Close()
		return nil, nil, err
	}

	// 在密钥补齐后执行完整配置校验，避免空 jwt.secret 导致服务运行时失败。
	if err := cfg.Validate(); err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("validate config after secret bootstrap: %w", err)
	}

	// SIMPLE 模式：启动时补齐各平台默认分组。
	// - anthropic/openai/gemini: 确保存在 <platform>-default
	// - antigravity: 仅要求存在 >=2 个未软删除分组（用于 claude/gemini 混合调度场景）
	if cfg.RunMode == config.RunModeSimple {
		seedCtx, seedCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer seedCancel()
		if err := ensureSimpleModeDefaultGroups(seedCtx, client); err != nil {
			_ = client.Close()
			return nil, nil, err
		}
		if err := ensureSimpleModeAdminConcurrency(seedCtx, client); err != nil {
			_ = client.Close()
			return nil, nil, err
		}
	}

	return client, drv.DB(), nil
}

func openPostgresEntDriver(cfg *config.Config, dsn string) (*entsql.Driver, error) {
	if cfg.Server.EnableServerTiming {
		connector, err := pq.NewConnector(dsn)
		if err != nil {
			return nil, err
		}
		return entsql.OpenDB(dialect.Postgres, sql.OpenDB(newServerTimingConnector(connector))), nil
	}
	return entsql.Open(dialect.Postgres, dsn)
}

func isPostgresDatabaseMissing(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr != nil && string(pqErr.Code) == "3D000"
}

var postgresDatabaseNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,62}$`)

func ensurePostgresDatabase(ctx context.Context, dbCfg config.DatabaseConfig, timezoneName string) error {
	if !postgresDatabaseNamePattern.MatchString(dbCfg.DBName) {
		return fmt.Errorf("invalid database name %q", dbCfg.DBName)
	}
	bootstrapCfg := dbCfg
	bootstrapCfg.DBName = "postgres"
	bootstrapDB, err := sql.Open("postgres", bootstrapCfg.DSNWithTimezone(timezoneName))
	if err != nil {
		return fmt.Errorf("open bootstrap database: %w", err)
	}
	defer func() { _ = bootstrapDB.Close() }()
	if err := bootstrapDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping bootstrap database: %w", err)
	}
	return ensurePostgresDatabaseExists(ctx, bootstrapDB, dbCfg.DBName)
}

func ensurePostgresDatabaseExists(ctx context.Context, bootstrapDB *sql.DB, dbName string) error {
	if !postgresDatabaseNamePattern.MatchString(dbName) {
		return fmt.Errorf("invalid database name %q", dbName)
	}
	var exists bool
	if err := bootstrapDB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", dbName).Scan(&exists); err != nil {
		return fmt.Errorf("check database existence: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := bootstrapDB.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
		return fmt.Errorf("create database %q: %w", dbName, err)
	}
	return nil
}
