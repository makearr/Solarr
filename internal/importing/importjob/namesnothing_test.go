package importjob_test

import (
	"context"

	"github.com/makearr/Solarr/internal/importing/batch"
)

// These fakes match nothing by name: the name stage is tested with its own importer.
func (*fittingImporter) NameMatches(_ context.Context, request batch.Request) []batch.NameMatch {
	return make([]batch.NameMatch, len(request.Units))
}

func (*recordingImporter) NameMatches(_ context.Context, request batch.Request) []batch.NameMatch {
	return make([]batch.NameMatch, len(request.Units))
}

func (*heldImporter) NameMatches(_ context.Context, request batch.Request) []batch.NameMatch {
	return make([]batch.NameMatch, len(request.Units))
}

func (*packingImporter) NameMatches(_ context.Context, request batch.Request) []batch.NameMatch {
	return make([]batch.NameMatch, len(request.Units))
}

func (*heldRecorder) NameMatches(_ context.Context, request batch.Request) []batch.NameMatch {
	return make([]batch.NameMatch, len(request.Units))
}
