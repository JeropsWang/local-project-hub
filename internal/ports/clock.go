package ports

import "time"

// Clock allows operation planning and expiry to use a controlled time source.
type Clock interface{ Now() time.Time }
