// OS package updates: check, install, hold and reboot.
package protocol

import "regexp"

// Package names and versions end up as apt/dnf arguments, so both sides validate them strictly
// (the leading character can't be "-", which rules out option injection).
var (
	packageRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._-]{0,199}$`)
	versionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+.~:_-]{0,127}$`)
)

func ValidPackage(name string) bool { return packageRe.MatchString(name) }
func ValidVersion(v string) bool    { return versionRe.MatchString(v) }

type OSPackage struct {
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Available string `json:"available"`
	Security  bool   `json:"security"`
}

type OSCheckResult struct {
	Manager        string      `json:"manager"` // apt | dnf
	Packages       []OSPackage `json:"packages"`
	Held           []string    `json:"held"`
	RebootRequired bool        `json:"reboot_required"`
}

type OSPackageSpec struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"` // exact version, which may be a downgrade; empty = latest
}

type OSInstallArgs struct {
	Scope    string          `json:"scope"` // all | security | packages
	Packages []OSPackageSpec `json:"packages,omitempty"`
	Reboot   string          `json:"reboot"` // never | if_required
}

type OSInstallResult struct {
	Check     OSCheckResult `json:"check"` // state after the install
	Rebooting bool          `json:"rebooting"`
}

type OSHoldArgs struct {
	Package string `json:"package"`
	Hold    bool   `json:"hold"`
}

// OSInstallPlan lists the steps an install reports.
var OSInstallPlan = []string{"Refreshing package lists", "Installing updates", "Checking the result"}
