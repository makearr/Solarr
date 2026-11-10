package mover

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/iogate"
)

// PlaceOneOnDisk is PlaceOne taken as a turn on the disks of both paths, when ctx belongs to a job that takes turns.
func PlaceOneOnDisk(ctx context.Context, fsys fsx.FS, sourceAbs, destAbs string, sizeBytes int64, ladder []PlaceMethod) (PlaceMethod, error) {
	var method PlaceMethod
	err := iogate.OnDisk(ctx, "", []string{sourceAbs, destAbs}, func() (placeErr error) {
		method, placeErr = PlaceOne(fsys, sourceAbs, destAbs, sizeBytes, ladder)
		return placeErr
	})
	return method, err
}
