package protocol

import (
	"fmt"
	"regexp"
)

var practiceSubdomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidatePracticeSubdomain applies the same conservative single-label rule in
// the CLI and HTTP boundary. Empty means no practice client context.
func ValidatePracticeSubdomain(value string) error {
	if value != "" && !practiceSubdomainPattern.MatchString(value) {
		return fmt.Errorf("practice subdomain must be one lowercase DNS label")
	}
	return nil
}
