package drivepool

import "github.com/Souvik-223/rhino-framework/drivepool/manifest"

// NewPoolForTesting builds a Pool directly from an already-open manifest
// and a pre-built accounts map, bypassing OAuth/token loading entirely.
//
// This intentionally lives in a regular (non-"_test.go") file: Go only
// compiles a package's "_test.go" files — including export_test.go-style
// seams — into that package's *own* test binary, never into a different
// package that merely imports it (such as backend/tests, a separate
// package in a separate directory). A cross-package test fixture like this
// one has to be regular exported API for other packages' tests to reach
// it at all. It is not used by any production code path — only by test
// packages (drivepool's own pool_test.go included) that need a Pool wired
// to a fake gdrive.RemoteStore instead of a real OAuth/Drive dependency.
func NewPoolForTesting(m *manifest.Manifest, userID string, accounts map[string]*Account) *Pool {
	return &Pool{manifest: m, userID: userID, accounts: accounts}
}
