package comic

import (
	"fmt"

	"github.com/makearr/Solarr/internal/identify/parsecomic"
)

// ReleaseLabel is what a release holds, for a queue or history row; empty when its name states no part.
func ReleaseLabel(releaseTitle string) string {
	claim := parsecomic.Parse(releaseTitle)
	switch {
	case claim.Volume > 0 && claim.Chapter == "" && claim.VolumeEnd > claim.Volume:
		return fmt.Sprintf("v%02d-v%02d", claim.Volume, claim.VolumeEnd)
	case claim.Volume > 0 && claim.Chapter == "":
		return fmt.Sprintf("v%02d", claim.Volume)
	case claim.Chapter != "" && claim.ChapterEnd != "":
		return "c" + claim.Chapter + "-c" + claim.ChapterEnd
	case claim.Chapter != "":
		return "c" + claim.Chapter
	case claim.IssueNumber != "" && claim.IssueEnd != "":
		return "#" + claim.IssueNumber + "-#" + claim.IssueEnd
	case claim.IssueNumber != "":
		return "#" + claim.IssueNumber
	case claim.Complete:
		return "Complete"
	}
	return ""
}
