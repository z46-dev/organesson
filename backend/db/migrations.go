package db

import (
	"github.com/z46-dev/golog"
	"github.com/z46-dev/gosqlite"
)

// migrateTable applies one schema migration and reports any structural changes.
func migrateTable[T any](table *gosqlite.RegisteredStruct[T], migrationOptions gosqlite.MigrationOptions, logger *golog.Logger) (err error) {
	var report *gosqlite.MigrationReport
	if report, err = table.Migrate(migrationOptions); err != nil || report == nil || logger == nil {
		return
	}
	if len(report.AddedColumns) > 0 {
		logger.Warningf("Added columns to table for %T: %v\n", new(T), report.AddedColumns)
	}
	if len(report.ChangedColumns) > 0 {
		logger.Warningf("Changed columns in table for %T: %v\n", new(T), report.ChangedColumns)
	}
	if len(report.DroppedColumns) > 0 {
		logger.Warningf("Dropped columns from table for %T: %v\n", new(T), report.DroppedColumns)
	}
	if len(report.RenamedColumns) > 0 {
		logger.Warningf("Renamed columns in table for %T: %v\n", new(T), report.RenamedColumns)
	}
	if report.Rebuilt {
		logger.Warningf("Rebuilt table for %T\n", new(T))
	}
	return
}
