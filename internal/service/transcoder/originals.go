package transcoder

import "context"

// capBatch bounds how many kept originals one pass over the size cap looks at.
const capBatch = 200

// EnforceCap purges the oldest kept originals until the bytes the bin holds for conversions are within the cap; 0 is no cap.
func (s *Service) EnforceCap(ctx context.Context) error {
	limit := s.Settings.Global().Originals.CapGB << 30
	if limit <= 0 {
		return nil
	}
	kept, err := s.Items.OriginalsKept(ctx)
	if err != nil {
		return err
	}
	for kept > limit {
		oldest, err := s.Items.OldestOriginals(ctx, capBatch)
		if err != nil || len(oldest) == 0 {
			return err
		}
		for _, original := range oldest {
			if kept <= limit {
				return nil
			}
			if err := s.Bin.Purge(ctx, original.RecycleID); err != nil {
				return describe("purge an original over the cap", err)
			}
			kept -= original.SizeBytes
		}
	}
	return nil
}
