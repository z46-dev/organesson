package db

import "github.com/z46-dev/gosqlite"

func migrateTable[T any](table *gosqlite.RegisteredStruct[T], migrationOpts gosqlite.MigrationOptions) (err error) {
	var report *gosqlite.MigrationReport
	if report, err = table.Migrate(migrationOpts); err == nil && report != nil {
		if len(report.AddedColumns) > 0 {
			log.Warningf("Added columns to table for %T: %v\n", new(T), report.AddedColumns)
		}

		if len(report.ChangedColumns) > 0 {
			log.Warningf("Changed columns in table for %T: %v\n", new(T), report.ChangedColumns)
		}

		if len(report.DroppedColumns) > 0 {
			log.Warningf("Dropped columns from table for %T: %v\n", new(T), report.DroppedColumns)
		}

		if len(report.RenamedColumns) > 0 {
			log.Warningf("Renamed columns in table for %T: %v\n", new(T), report.RenamedColumns)
		}

		if report.Rebuilt {
			log.Warningf("Rebuilt table for %T\n", new(T))
		}
	}

	return
}
