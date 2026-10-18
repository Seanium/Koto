//go:build !linux

package update

import (
	"context"
	"fmt"
)

type PackageInstallation struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Executable   string `json:"executable"`
}
type PackagePrepared struct {
	Location        Location            `json:"location"`
	Installed       PackageInstallation `json:"installed"`
	NewVersion      string              `json:"newVersion"`
	NewDEB          string              `json:"newDEB"`
	OldDEB          string              `json:"oldDEB"`
	NewSHA256       string              `json:"newSHA256"`
	OldSHA256       string              `json:"oldSHA256"`
	BeforeSHA256    string              `json:"beforeSHA256"`
	TrialExecutable string              `json:"trialExecutable"`
	NeedsRecovery   bool                `json:"needsRecovery"`
}

func DetectPackage(context.Context, Location) (*PackageInstallation, error) { return nil, nil }
func (c *Client) PreparePackage(context.Context, *Result, *Result, Location, string, func(int64, int64)) (*PackagePrepared, error) {
	return nil, fmt.Errorf("DEB package updates require Linux")
}
func (p *PackagePrepared) Validate(context.Context) error {
	return fmt.Errorf("DEB package updates require Linux")
}
func (p *PackagePrepared) Install(context.Context) error {
	return fmt.Errorf("DEB package updates require Linux")
}
func (p *PackagePrepared) Rollback(context.Context) error {
	return fmt.Errorf("DEB package updates require Linux")
}
