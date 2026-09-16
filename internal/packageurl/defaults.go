package packageurl

import "time"

// DefaultSignedURLTTL is long enough for a host to review and execute a
// materialisation response while keeping package URLs short-lived.
const DefaultSignedURLTTL = 30 * time.Minute
