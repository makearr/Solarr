package iogate

import (
	"context"
	"io"
)

// Reader makes every Read of rs a turn on the disk under path, so a viewer who pauses holds nothing between reads and
// one who plays gets the disk ahead of the bulk work.
func Reader(ctx context.Context, kind, path string, rs io.ReadSeeker) io.ReadSeeker {
	return turnReader{ctx: ctx, kind: kind, path: path, ReadSeeker: rs}
}

type turnReader struct {
	ctx  context.Context
	kind string
	path string
	io.ReadSeeker
}

func (r turnReader) Read(p []byte) (n int, err error) {
	turnErr := OnDisk(r.ctx, r.kind, []string{r.path}, func() error {
		n, err = r.ReadSeeker.Read(p)
		return nil
	})
	if turnErr != nil {
		return 0, turnErr
	}
	return n, err
}
