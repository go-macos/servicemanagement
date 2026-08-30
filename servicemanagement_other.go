// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

//go:build !darwin

package servicemanagement

// init points the seams at stubs on platforms with no ServiceManagement
// framework, and sets loadErr so every operation reports [ErrUnsupported]
// before it can reach one.
//
// Note what is NOT stubbed out: the service identity, the name validation, the
// status typing and the advice. Those are in the portable file and behave here
// exactly as they do on macOS, which is what lets every branch of them be
// tested on a runner with no macOS anywhere.
//
// The stubs are still assigned rather than left nil. A nil seam is a panic
// waiting for the day the guard order changes, and a stub that answers "no" is
// the same information without the crash.
func init() {
	loadErr = ErrUnsupported
	available = func() bool { return false }
	bundleID = func() string { return "" }
	doRegister = func(kind, string) error { return ErrUnsupported }
	doUnregister = func(kind, string) error { return ErrUnsupported }
	doStatus = func(kind, string) (int, error) { return 0, ErrUnsupported }
}
