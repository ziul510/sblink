package main

import (
	"context"
	"os"
	"time"
)

func lockFileContext(ctx context.Context, path string) (*os.File, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := lockFile(path, false)
		if err == nil {
			return file, nil
		}
		if !lockBusy(err) {
			return nil, err
		}
		if err := sleepContext(ctx, 25*time.Millisecond); err != nil {
			return nil, err
		}
	}
}
