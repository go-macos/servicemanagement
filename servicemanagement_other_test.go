// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

//go:build !darwin

package servicemanagement

import (
	"errors"
	"testing"
)

// The non-darwin half. What matters here is that a consumer cross-compiles and
// gets a CLEAR error rather than a link failure or a silent no-op — and that
// the portable half is still doing its work, so a developer on Linux is told
// about a malformed service name here instead of on the Mac where it would
// finally be built.

func TestEveryOperationIsUnsupported(t *testing.T) {
	for _, svc := range []Service{
		MainApp(),
		Agent("io.example.godl.plist"),
		Daemon("io.example.d.plist"),
		LoginItem("io.example.helper"),
	} {
		if err := svc.Register(); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: Register = %v, want ErrUnsupported", svc, err)
		}
		if err := svc.Unregister(); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: Unregister = %v, want ErrUnsupported", svc, err)
		}
		st, err := svc.Status()
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: Status = %v, want ErrUnsupported", svc, err)
		}
		// Not NotRegistered, and not NotFound: neither would be true, and
		// both would read as a fact about a service rather than about the
		// platform.
		if st != 0 {
			t.Errorf("%s: a status was invented off darwin: %v", svc, st)
		}
	}
}

func TestNothingIsBundledHere(t *testing.T) {
	// There are no application bundles on this platform, so the honest answer
	// is no — not an error, because a caller uses this to CHOOSE a mechanism,
	// and a caller choosing the plist fallback should not have to handle an
	// error to do it.
	if id, ok := Bundled(); ok || id != "" {
		t.Errorf("Bundled = %q, %v", id, ok)
	}
}

// TestTheSeamsAnswerRatherThanPanic covers the stubs directly.
//
// The exported API never reaches them — loadErr short-circuits first — so
// without this they would be code nothing runs, and the day the guard order
// changes they would be a nil-function panic instead of an error. A stub that
// answers is the same information without the crash.
func TestTheSeamsAnswerRatherThanPanic(t *testing.T) {
	if available() {
		t.Error("SMAppService was reported available off darwin")
	}
	if id := bundleID(); id != "" {
		t.Errorf("bundleID = %q off darwin", id)
	}
	if err := doRegister(kindAgent, "x.plist"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("doRegister = %v", err)
	}
	if err := doUnregister(kindAgent, "x.plist"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("doUnregister = %v", err)
	}
	raw, err := doStatus(kindAgent, "x.plist")
	if !errors.Is(err, ErrUnsupported) || raw != 0 {
		t.Errorf("doStatus = %d, %v", raw, err)
	}
}
