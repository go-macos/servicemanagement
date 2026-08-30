// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package servicemanagement

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-macos/objc"
)

// fake is a whole ServiceManagement, arranged. Every field is what the
// corresponding seam should answer, and calls records what the package asked
// for — so a test can assert not only the answer a caller gets but that the
// right service was named on the way, which is the failure nobody would
// otherwise see: a Register that succeeds against the wrong plist.
type fake struct {
	present  bool
	bundle   string
	regErr   error
	unregErr error
	status   int
	statErr  error

	calls []string
}

// install points the package at f for the duration of the test, and restores
// the real (or stub) seams afterwards, so a darwin run of this suite leaves
// nothing pointed at a fake.
func (f *fake) install(t *testing.T) *fake {
	t.Helper()
	savedLoad, savedAvail, savedBundle := loadErr, available, bundleID
	savedReg, savedUnreg, savedStat := doRegister, doUnregister, doStatus
	t.Cleanup(func() {
		loadErr, available, bundleID = savedLoad, savedAvail, savedBundle
		doRegister, doUnregister, doStatus = savedReg, savedUnreg, savedStat
	})

	note := func(op string, k kind, name string) {
		f.calls = append(f.calls, op+" "+Service{kind: k, name: name}.String())
	}
	loadErr = nil
	available = func() bool { return f.present }
	bundleID = func() string { return f.bundle }
	doRegister = func(k kind, name string) error { note("register", k, name); return f.regErr }
	doUnregister = func(k kind, name string) error { note("unregister", k, name); return f.unregErr }
	doStatus = func(k kind, name string) (int, error) {
		note("status", k, name)
		return f.status, f.statErr
	}
	return f
}

// working is the ordinary machine: macOS 13 or later, running in a bundle.
func working() *fake { return &fake{present: true, bundle: "io.example.godl"} }

// TestAStatusSaysWhatItMeans covers the typing of SMAppServiceStatus. The
// numbers are Apple's and cross the bridge as bare integers, so a mis-ordered
// constant here would report "enabled" for a service the person has switched
// off — a program that starts nothing and swears it is fine.
func TestAStatusSaysWhatItMeans(t *testing.T) {
	for _, tc := range []struct {
		st         Status
		raw        int
		name       string
		registered bool
		running    bool
		advises    bool
	}{
		{NotRegistered, 0, "notRegistered", false, false, true},
		{Enabled, 1, "enabled", true, true, false},
		{RequiresApproval, 2, "requiresApproval", true, false, true},
		{NotFound, 3, "notFound", false, false, true},
	} {
		if int(tc.st) != tc.raw {
			t.Errorf("%s is %d, want Apple's %d", tc.name, int(tc.st), tc.raw)
		}
		if got := tc.st.String(); got != tc.name {
			t.Errorf("String = %q, want %q", got, tc.name)
		}
		if got := tc.st.Registered(); got != tc.registered {
			t.Errorf("%s.Registered = %v, want %v", tc.name, got, tc.registered)
		}
		if got := tc.st.Running(); got != tc.running {
			t.Errorf("%s.Running = %v, want %v", tc.name, got, tc.running)
		}
		if got := tc.st.Advice() != ""; got != tc.advises {
			t.Errorf("%s has advice = %v, want %v", tc.name, got, tc.advises)
		}
	}

	// A status macOS invents that we do not know is shown, not guessed at.
	// Pretending an unknown number is NotRegistered would be this package
	// answering a question it was not told the answer to.
	unknown := Status(42)
	if got := unknown.String(); !strings.Contains(got, "42") {
		t.Errorf("an unknown status renders as %q, which does not say which one", got)
	}
	if unknown.Registered() || unknown.Running() || unknown.Advice() != "" {
		t.Error("an unknown status was treated as one of the known ones")
	}
}

// TestApprovalIsSaidOutLoud is the whole reason Status is a value rather than
// an error. A registration that leaves the service switched off runs nothing,
// and the only place a person can act is a Settings pane they will not find by
// guessing — so the advice must name it.
func TestApprovalIsSaidOutLoud(t *testing.T) {
	advice := RequiresApproval.Advice()
	for _, want := range []string{"System Settings", "Login Items"} {
		if !strings.Contains(advice, want) {
			t.Errorf("the advice for requiresApproval does not mention %q: %q", want, advice)
		}
	}
	// And it is not an error: a caller must be able to see it after a Register
	// that returned nil.
	f := (&fake{present: true, bundle: "io.example.godl", status: int(RequiresApproval)}).install(t)
	svc := MainApp()
	if err := svc.Register(); err != nil {
		t.Fatalf("Register = %v, want nil (approval pending is not a failure)", err)
	}
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st != RequiresApproval {
		t.Fatalf("status = %v, want requiresApproval", st)
	}
	if !st.Registered() || st.Running() {
		t.Error("a service awaiting approval is registered and not running")
	}
	_ = f
}

// TestAServiceNamesItself covers the four factories. Each is a different class
// message on the other side, and the name means a different thing in each — a
// file name for two of them, a bundle identifier for the third, nothing at all
// for the fourth.
func TestAServiceNamesItself(t *testing.T) {
	for _, tc := range []struct {
		svc  Service
		name string
		says string
	}{
		{MainApp(), "", "the main application"},
		{Agent("io.example.godl.plist"), "io.example.godl.plist", "agent io.example.godl.plist"},
		{Daemon("io.example.d.plist"), "io.example.d.plist", "daemon io.example.d.plist"},
		{LoginItem("io.example.helper"), "io.example.helper", "login item io.example.helper"},
	} {
		if got := tc.svc.Name(); got != tc.name {
			t.Errorf("Name = %q, want %q", got, tc.name)
		}
		if got := tc.svc.String(); got != tc.says {
			t.Errorf("String = %q, want %q", got, tc.says)
		}
	}
	// The zero Service is the main application, so a caller that forgot to
	// call a constructor gets the sensible thing rather than a wild message.
	if (Service{}) != MainApp() {
		t.Error("the zero Service is not the main application")
	}
	// A kind nobody wrote is described rather than silently treated as the
	// main application, which would register the WRONG thing.
	if got := (Service{kind: kind(99), name: "x"}).String(); !strings.Contains(got, "99") {
		t.Errorf("an unknown kind renders as %q", got)
	}
}

// TestTheGuardComplainsInTheRightOrder covers check. The order is the point:
// each answer is only useful when the ones before it have been ruled out, and
// "your macOS is too old" told to a Linux user is worse than no message.
func TestTheGuardComplainsInTheRightOrder(t *testing.T) {
	t.Run("wrong platform", func(t *testing.T) {
		f := working().install(t)
		loadErr = ErrUnsupported
		for _, err := range allThree(t, MainApp()) {
			if !errors.Is(err, ErrUnsupported) {
				t.Errorf("= %v, want ErrUnsupported", err)
			}
		}
		if len(f.calls) != 0 {
			t.Errorf("the platform was reached anyway: %v", f.calls)
		}
		if id, ok := Bundled(); ok || id != "" {
			t.Errorf("Bundled = %q, %v off the supported platform", id, ok)
		}
	})

	t.Run("a name that cannot cross the bridge", func(t *testing.T) {
		f := working().install(t)
		// Empty and NUL-bearing names both reach ObjC as +stringWithUTF8String:,
		// which stops at the first NUL: such a name would be SHORTENED into
		// some other service's name rather than refused.
		if err := Agent("").Register(); !errors.Is(err, objc.ErrEmptyName) {
			t.Errorf("an agent with no plist name = %v, want ErrEmptyName", err)
		}
		if err := Agent("io.example\x00.plist").Register(); !errors.Is(err, objc.ErrNameHasNUL) {
			t.Errorf("a plist name with a NUL = %v, want ErrNameHasNUL", err)
		}
		if err := LoginItem("").Unregister(); !errors.Is(err, objc.ErrEmptyName) {
			t.Errorf("a login item with no identifier = %v, want ErrEmptyName", err)
		}
		if _, err := Daemon("").Status(); !errors.Is(err, objc.ErrEmptyName) {
			t.Errorf("a daemon with no plist name = %v, want ErrEmptyName", err)
		}
		if len(f.calls) != 0 {
			t.Errorf("a name that cannot be carried was carried anyway: %v", f.calls)
		}
		// The main application is named by the bundle, so it has no name to
		// validate and must not be refused for lacking one.
		if err := MainApp().Register(); err != nil {
			t.Errorf("the main application was refused for having no name: %v", err)
		}
	})

	t.Run("a macOS with no SMAppService", func(t *testing.T) {
		f := (&fake{present: false, bundle: "io.example.godl"}).install(t)
		for _, err := range allThree(t, MainApp()) {
			if !errors.Is(err, ErrTooOld) {
				t.Errorf("= %v, want ErrTooOld", err)
			}
		}
		if len(f.calls) != 0 {
			t.Errorf("SMAppService was called on a macOS that has none: %v", f.calls)
		}
	})

	t.Run("no bundle to be identified by", func(t *testing.T) {
		f := (&fake{present: true, bundle: ""}).install(t)
		for _, err := range allThree(t, Agent("io.example.godl.plist")) {
			if !errors.Is(err, ErrNotBundled) {
				t.Errorf("= %v, want ErrNotBundled", err)
			}
		}
		if len(f.calls) != 0 {
			t.Errorf("SMAppService was reached without a bundle: %v", f.calls)
		}
		if id, ok := Bundled(); ok || id != "" {
			t.Errorf("Bundled = %q, %v outside a bundle", id, ok)
		}
	})

	t.Run("everything in place", func(t *testing.T) {
		(&fake{present: true, bundle: "io.example.godl"}).install(t)
		id, ok := Bundled()
		if !ok || id != "io.example.godl" {
			t.Errorf("Bundled = %q, %v; want the identifier", id, ok)
		}
	})
}

// allThree runs the three operations and hands back their errors, so a guard
// can be asserted once for all of them instead of three times over.
func allThree(t *testing.T, svc Service) []error {
	t.Helper()
	_, statErr := svc.Status()
	return []error{svc.Register(), svc.Unregister(), statErr}
}

// TestTheOperationsReachTheRightService covers the happy paths, and that the
// service named on the way in is the one named on the way out. A Register that
// succeeds against the wrong plist is the failure that looks like success.
func TestTheOperationsReachTheRightService(t *testing.T) {
	f := (&fake{present: true, bundle: "io.example.godl", status: int(Enabled)}).install(t)
	svc := Agent("io.example.godl.plist")

	if err := svc.Register(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Unregister(); err != nil {
		t.Fatal(err)
	}
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st != Enabled {
		t.Errorf("status = %v, want enabled", st)
	}
	want := []string{
		"register agent io.example.godl.plist",
		"unregister agent io.example.godl.plist",
		"status agent io.example.godl.plist",
	}
	if len(f.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
	for i := range want {
		if f.calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, f.calls[i], want[i])
		}
	}
}

// TestWhenMacOSRefuses covers the failure paths. Each is a real refusal from a
// working machine, which is the only kind this package cannot arrange for
// itself.
func TestWhenMacOSRefuses(t *testing.T) {
	boom := &SystemError{
		Op: "register", Service: "agent io.example.godl.plist",
		Domain: "SMAppServiceErrorDomain", Code: 108,
		Message: "The operation couldn’t be completed. Unable to read plist: io.example.godl.plist",
	}
	f := working().install(t)
	f.regErr, f.unregErr, f.statErr = boom, ErrNoService, ErrNoService

	svc := Agent("io.example.godl.plist")
	var se *SystemError
	if err := svc.Register(); !errors.As(err, &se) || se.Code != 108 {
		t.Fatalf("Register = %v, want the SystemError through", err)
	}
	if err := svc.Unregister(); !errors.Is(err, ErrNoService) {
		t.Errorf("Unregister = %v, want ErrNoService", err)
	}
	// A status that could not be read must not be reported as a status. Zero
	// happens to be notRegistered, and answering "not registered" to a
	// question nobody could ask is the lie this guards against.
	st, err := svc.Status()
	if !errors.Is(err, ErrNoService) {
		t.Errorf("Status = %v, want ErrNoService", err)
	}
	if st != 0 {
		t.Errorf("a failed Status handed back %v", st)
	}

	// The rendered error must carry the three facts that distinguish one
	// refusal from another: which operation, which service, and macOS's own
	// words for what went wrong.
	msg := boom.Error()
	for _, want := range []string{"register", "io.example.godl.plist", "Unable to read plist", "SMAppServiceErrorDomain", "108"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not say %q: %s", want, msg)
		}
	}
}

// TestTheFrameworkPathIsTheRealOne guards the one constant nothing else would
// notice: a wrong path makes objc.Load fail, the class stay nil, and every
// operation report ErrTooOld on a macOS that is not too old at all.
func TestTheFrameworkPathIsTheRealOne(t *testing.T) {
	if !strings.HasSuffix(Framework, "/ServiceManagement.framework/ServiceManagement") {
		t.Errorf("Framework = %q", Framework)
	}
}
