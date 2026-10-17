package main

import "koto/internal/legal"

// LicenseNotices returns the complete notices embedded in the standalone app.
func (s *AppService) LicenseNotices() (string, error) { return legal.Notices() }
