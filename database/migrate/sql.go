package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

var sqlFileName = regexp.MustCompile(`^(\d+)[_-](.+)\.(up|down)\.sql$`)

// LoadSQL 从文件系统加载 SQL 迁移。
//
// 文件命名：
//
//	0001_create_users.up.sql
//	0001_create_users.down.sql
//	0002_add_email.up.sql
//
// 每个文件建议只包含一条 SQL；多语句能力取决于数据库驱动。
// Up 文件必须存在，Down 文件可选；缺少 Down 的迁移执行 Down 时会返回 ErrNoDown。
func LoadSQL(fsys fs.FS, dir string) ([]Migration, error) {
	if fsys == nil {
		return nil, fmt.Errorf("migrate: filesystem is nil")
	}
	if dir == "" {
		dir = "."
	}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: read dir %q: %w", dir, err)
	}

	type sqlMigration struct {
		version int64
		name    string
		up      string
		down    string
	}
	byVersion := make(map[int64]*sqlMigration)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := sqlFileName.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migrate: invalid version in %q", entry.Name())
		}
		name := strings.ReplaceAll(matches[2], "_", " ")
		direction := matches[3]

		data, err := fs.ReadFile(fsys, path.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("migrate: read %q: %w", entry.Name(), err)
		}
		content := string(data)

		item, ok := byVersion[version]
		if !ok {
			item = &sqlMigration{version: version, name: name}
			byVersion[version] = item
		}
		switch direction {
		case "up":
			if item.up != "" {
				return nil, fmt.Errorf("%w: duplicate up file for version %d", ErrDuplicateVersion, version)
			}
			item.up = content
		case "down":
			if item.down != "" {
				return nil, fmt.Errorf("%w: duplicate down file for version %d", ErrDuplicateVersion, version)
			}
			item.down = content
		}
	}

	versions := make([]int64, 0, len(byVersion))
	for version := range byVersion {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })

	migrations := make([]Migration, 0, len(versions))
	for _, version := range versions {
		item := byVersion[version]
		if strings.TrimSpace(item.up) == "" {
			return nil, fmt.Errorf("migrate: missing up file for version %d", version)
		}
		m := Migration{
			Version: item.version,
			Name:    item.name,
			Up: func(ctx context.Context, tx *gorm.DB) error {
				return tx.WithContext(ctx).Exec(item.up).Error
			},
		}
		if strings.TrimSpace(item.down) != "" {
			down := item.down
			m.Down = func(ctx context.Context, tx *gorm.DB) error {
				return tx.WithContext(ctx).Exec(down).Error
			}
		}
		migrations = append(migrations, m)
	}
	return migrations, nil
}
