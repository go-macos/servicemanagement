// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

//go:build darwin

package servicemanagement

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-macos/objc"
)

// The LIVE suite: the real ServiceManagement framework, on this machine.
//
// It calls the seams directly rather than the exported API, on purpose. The
// exported API stops at [ErrNotBundled] for a `go test` binary — which is
// correct, and which would leave every Objective-C call in this package
// untouched by any test. Underneath the guard, the calls can still be made, and
// they answer.
//
// Nothing here changes anything on the machine. Every service named is one that
// does not exist, so:
//
//   - -status answers notFound, which is a read;
//   - -register: fails before it registers anything (there is no plist to
//     read), which is the error path this suite is here to exercise;
//   - -unregister: fails for the same reason.
//
// The one thing a live suite must never do is leave a login item behind on the
// machine that ran it, and the way that is guaranteed here is by never naming a
// service that could be registered in the first place.

// missing is a plist name no bundle has, and none will.
const missing = "io.example.go-macos.servicemanagement.doesnotexist.plist"

// requireSMAppService skips when this macOS predates SMAppService, so the
// suite fails for its own reasons or not at all.
func requireSMAppService(t *testing.T) {
	t.Helper()
	if !smAvailable() {
		t.Skip("this macOS has no SMAppService (macOS 13 or later is required)")
	}
}

// TestTheFrameworkIsReallyThere is the negative control for every other test
// in this file: if the class did not resolve, all of them would skip and the
// suite would pass having exercised nothing.
func TestTheFrameworkIsReallyThere(t *testing.T) {
	requireSMAppService(t)
	if objc.ClassID("SMAppService") == 0 {
		t.Fatal("smAvailable said yes and the class is nil")
	}
}

// TestATestBinaryIsNotBundled pins the fact the whole package is built around.
// A `go test` binary is a bare executable, so it has no bundle identifier, and
// the exported API must refuse rather than let macOS answer notFound to
// everything.
func TestATestBinaryIsNotBundled(t *testing.T) {
	requireSMAppService(t)
	if id := mainBundleIdentifier(); id != "" {
		t.Fatalf("a test binary reported bundle identifier %q", id)
	}
	if id, ok := Bundled(); ok || id != "" {
		t.Errorf("Bundled = %q, %v for a bare executable", id, ok)
	}
	for _, err := range allThree(t, Agent(missing)) {
		if !errors.Is(err, ErrNotBundled) {
			t.Errorf("= %v, want ErrNotBundled", err)
		}
	}
}

// TestEveryFactoryAnswers covers the four class messages. Each is a different
// selector, and a typo in one of them is a service object that is nil — which
// would surface as ErrNoService for that one kind only, in a program that does
// not use the other three.
func TestEveryFactoryAnswers(t *testing.T) {
	requireSMAppService(t)
	for _, tc := range []struct {
		k    kind
		name string
	}{
		{kindMainApp, ""},
		{kindAgent, missing},
		{kindDaemon, missing},
		{kindLoginItem, "io.example.go-macos.servicemanagement.nothing"},
	} {
		svc := Service{kind: tc.k, name: tc.name}
		raw, err := statusOfService(tc.k, tc.name)
		if err != nil {
			t.Errorf("%s: status: %v", svc, err)
			continue
		}
		// Nothing here exists, so notFound is the only honest answer. Any
		// other one means the selector reached a service that does exist,
		// which this suite must never touch.
		if Status(raw) != NotFound {
			t.Errorf("%s: status = %v, want notFound", svc, Status(raw))
		}
	}
}

// TestAKindNobodyWroteIsNotAMessage covers the guard that keeps a bad kind from
// becoming a message to nil that answers zero and looks like "not registered".
func TestAKindNobodyWroteIsNotAMessage(t *testing.T) {
	requireSMAppService(t)
	bogus := kind(99)
	if obj := serviceObject(bogus, ""); obj != 0 {
		t.Fatalf("an unknown kind produced an object: %v", obj)
	}
	if _, err := statusOfService(bogus, ""); !errors.Is(err, ErrNoService) {
		t.Errorf("status of an unknown kind = %v, want ErrNoService", err)
	}
	if err := operate("register", bogus, "", objc.Sel("registerAndReturnError:")); !errors.Is(err, ErrNoService) {
		t.Errorf("register of an unknown kind = %v, want ErrNoService", err)
	}
}

// TestMacOSRefusesInItsOwnWords covers the NSError path: the out-parameter, the
// three reads from the error object, and the fact that a refusal really does
// arrive as one rather than as a silent false.
//
// It is also where the interesting detail lives. Run from a bare executable the
// refusal is "Codesigning failure loading plist" (code 3) — macOS blaming the
// signature for what is really a missing bundle identifier, which is precisely
// why this package checks for the bundle itself and reports ErrNotBundled.
func TestMacOSRefusesInItsOwnWords(t *testing.T) {
	requireSMAppService(t)

	err := registerService(kindAgent, missing)
	if err == nil {
		t.Fatal("registering a plist that does not exist SUCCEEDED; the machine now has a service this test did not intend")
	}
	var se *SystemError
	if !errors.As(err, &se) {
		t.Fatalf("register = %v (%T), want a *SystemError", err, err)
	}
	if se.Op != "register" || !strings.Contains(se.Service, missing) {
		t.Errorf("the error does not name what failed: %+v", se)
	}
	if se.Domain == "" || se.Message == "" {
		t.Errorf("macOS said nothing usable: %+v", se)
	}
	t.Logf("register refused: %v", se)

	err = unregisterService(kindAgent, missing)
	if err == nil {
		t.Fatal("unregistering a service that was never registered succeeded")
	}
	if !errors.As(err, &se) {
		t.Fatalf("unregister = %v (%T), want a *SystemError", err, err)
	}
	if se.Op != "unregister" {
		t.Errorf("the error is tagged %q", se.Op)
	}
	t.Logf("unregister refused: %v", se)
}

// TestAFailureWithNothingSaid covers the branch where macOS returns NO and
// leaves the NSError nil. The method contract only promises the error object;
// nothing enforces it, and dereferencing a nil one would turn a refusal into a
// message about nothing.
func TestAFailureWithNothingSaid(t *testing.T) {
	err := systemError("register", kindAgent, missing, 0)
	var se *SystemError
	if !errors.As(err, &se) {
		t.Fatalf("= %v (%T), want a *SystemError", err, err)
	}
	if se.Code != 0 || se.Domain != "" {
		t.Errorf("a code and domain were invented: %+v", se)
	}
	if !strings.Contains(se.Error(), "gave no reason") {
		t.Errorf("the error does not admit it has no reason: %v", se)
	}
}
