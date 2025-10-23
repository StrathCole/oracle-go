// Package version provides version information for the oracle-go application.
package version

// Version is the current version of the oracle-go application.
const Version = "1.0.2"

// AgentString returns the full agent string with versioning.
// Format: @classic-terra/oracle-go@v{version}
func AgentString() string {
	return "@classic-terra/oracle-go@v" + Version
}
