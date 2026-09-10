package privilege

import (
	"fmt"
	"io"
	"os"
	"time"
)

// DefaultTimezone is what an unset TZ means.
const DefaultTimezone = "UTC"

// ApplyTimezone sets the process timezone from TZ. See
// documentation/container-runtime.md.
func ApplyTimezone(lookup Lookup, out io.Writer) string {
	name, set := lookup("TZ")
	if !set || name == "" {
		return DefaultTimezone
	}

	location, err := time.LoadLocation(name)
	if err != nil {
		// A display preference is not worth refusing to start over.
		_, _ = fmt.Fprintf(out, "warning: TZ=%q is not a known timezone; using %s\n\n",
			name, DefaultTimezone)
		return DefaultTimezone
	}

	time.Local = location
	// Child processes and anything reading the environment directly.
	_ = os.Setenv("TZ", name)
	return name
}
