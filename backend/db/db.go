package db

import "github.com/z46-dev/golog"

var log *golog.Logger

func Init(logger *golog.Logger) (err error) {
	log = logger

	return
}
