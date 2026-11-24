package transcoder

import (
	"context"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
)

// FreshPlan reads the file now and judges it against the current target and setup, for a person looking at one file.
// False means no fresh answer: the file is not judged, is being converted, is gone or unreadable, or its library is off.
func (s *Service) FreshPlan(ctx context.Context, mediaObjectID int64) (Judgement, bool, error) {
	item, found, err := s.Items.Get(ctx, mediaObjectID)
	if err != nil || !found || item.State == library.TranscodeRunning || item.State == library.TranscodeDone {
		return Judgement{}, false, err
	}
	libraries, err := s.Settings.Libraries(ctx)
	if err != nil {
		return Judgement{}, false, err
	}
	for _, one := range libraries {
		if one.CategoryID != item.CategoryID || !one.Enabled || !one.Supported() {
			continue
		}
		if one.Kind() == KindComic {
			return Judgement{}, false, nil
		}
		stored, err := s.Settings.Setup(ctx, one.Target.ID)
		if err != nil {
			return Judgement{}, false, err
		}
		paths, err := s.Items.Paths(ctx, mediaObjectID)
		if err != nil || len(paths) != 1 || paths[0].Missing {
			return Judgement{}, false, err
		}
		root, known := s.Settings.RootFolder(paths[0].RootFolderID)
		if !known || !root.Online {
			return Judgement{}, false, nil
		}
		source, err := s.probe(ctx, path.Join(root.Path, paths[0].RelPath))
		if err != nil {
			return Judgement{}, false, nil //nolint:nilerr // an unreadable file has no fresh plan; the stored one stays
		}
		plan := Analyze(source, *one.Target, stored.Setup, WantedLanguages(*one.Target, s.Settings.Languages(item.CategoryID)))
		judgement, err := Judge(plan, s.laneEngine(plan, *one.Target, stored.Setup))
		return judgement, err == nil, err
	}
	return Judgement{}, false, nil
}
