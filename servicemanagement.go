// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package servicemanagement

import (
	"errors"
	"fmt"

	"github.com/go-macos/objc"
)

// Framework is the ServiceManagement framework's path, opened by the darwin
// half on first use. It is exported because a caller that loads frameworks of
// its own has one list to keep, not two.
const Framework = "/System/Library/Frameworks/ServiceManagement.framework/ServiceManagement"

// Sentinel errors. They are stable and may be compared with [errors.Is].
var (
	// ErrUnsupported is returned by every operation on non-darwin platforms.
	// SMAppService is macOS-only; the symbols exist everywhere so consumers
	// cross-compile.
	ErrUnsupported = errors.New("servicemanagement: unsupported on this platform (darwin only)")

	// ErrNotBundled reports that this process is not inside an application
	// bundle, so it has no bundle identifier for SMAppService to register.
	//
	// It is separate from every other error here because it is the one that
	// actually happens, and because macOS hides it: outside a bundle the
	// factory methods still return objects and -status still answers NotFound,
	// so the only native symptom is a register failure blaming code signing.
	// Build the bundle with github.com/go-macos/appbundle.
	ErrNotBundled = errors.New("servicemanagement: this process is not in an application bundle (SMAppService identifies its caller by bundle identifier)")

	// ErrTooOld reports that SMAppService does not exist in this process:
	// the class is absent, which means macOS older than 13 (Ventura).
	// A caller that must run on both keeps github.com/go-macos/launchagent as
	// its fallback.
	ErrTooOld = errors.New("servicemanagement: SMAppService is not available (macOS 13 or later is required)")

	// ErrNoService reports that the ServiceManagement factory yielded nil for
	// this service. The service cannot be spoken about at all, which is not
	// the same as it being absent — that is reported as the [NotFound] status.
	ErrNoService = errors.New("servicemanagement: SMAppService returned no object for this service")
)

// Status is SMAppServiceStatus: what macOS currently thinks of a service.
//
// The numeric values are Apple's, not ours: they cross the ObjC boundary as an
// NSInteger and are pinned here so a mis-ordered constant cannot turn "enabled"
// into "needs approval" silently.
type Status int

const (
	// NotRegistered means the service is known and has never been registered,
	// or has been unregistered. Nothing will run.
	NotRegistered Status = 0
	// Enabled means the service is registered and allowed to run. This is the
	// one outcome that needs nothing said to anybody.
	Enabled Status = 1
	// RequiresApproval means the service is registered and will NOT run until
	// a person allows it in System Settings. It is a normal outcome of a
	// successful [Service.Register] — see [Status.Advice].
	RequiresApproval Status = 2
	// NotFound means macOS has no such service: no plist of that name in the
	// bundle, or a bundle it does not recognise. It is also what a process
	// outside a bundle sees for everything, which is why the operations here
	// refuse to reach this far without one.
	NotFound Status = 3
)

// String names the status the way Apple's own constant does.
func (s Status) String() string {
	switch s {
	case NotRegistered:
		return "notRegistered"
	case Enabled:
		return "enabled"
	case RequiresApproval:
		return "requiresApproval"
	case NotFound:
		return "notFound"
	}
	return fmt.Sprintf("Status(%d)", int(s))
}

// Registered reports whether macOS holds a registration for the service —
// [Enabled] or [RequiresApproval]. It is the question "did my Register take",
// which is not the same as "will it run": a service awaiting approval is
// registered and idle.
func (s Status) Registered() bool { return s == Enabled || s == RequiresApproval }

// Running reports whether the service is actually allowed to start, which is
// [Enabled] alone.
func (s Status) Running() bool { return s == Enabled }

// Advice is the sentence to show a person, or "" when there is nothing they
// need to do.
//
// It exists so that [RequiresApproval] cannot be handled by silence. A program
// that registered successfully and starts nothing has to say why, and the only
// place the person can act is a Settings pane they will not find by guessing.
func (s Status) Advice() string {
	switch s {
	case RequiresApproval:
		return "This item is registered but switched off. Allow it in System Settings > General > Login Items & Extensions, under this application's name."
	case NotRegistered:
		return "This item is not registered to start at login; register it first."
	case NotFound:
		return "macOS has no such service: check that the plist is inside the bundle (Contents/Library/LaunchAgents) and that the name matches its file name exactly."
	}
	return ""
}

// kind is which SMAppService factory makes the service. It is unexported: the
// four constructors below are the whole vocabulary, and an integer a caller
// could invent is a class message nobody wrote.
type kind int

const (
	kindMainApp kind = iota
	kindAgent
	kindDaemon
	kindLoginItem
)

// Service is one registrable service: the application itself, one of the
// agents or daemons its bundle ships, or a login item belonging to it.
//
// The zero Service is [MainApp]. It is a value, so it costs nothing to keep and
// nothing to release; the Objective-C object is made and dropped inside each
// operation.
type Service struct {
	kind kind
	// name is the plist file name, or the login item's bundle identifier.
	// Empty for the main application, which is named by the bundle itself.
	name string
}

// MainApp is the application this process is in: +[SMAppService mainAppService].
// Registering it is what "open at login" means for an application.
func MainApp() Service { return Service{kind: kindMainApp} }

// Agent is a per-user LaunchAgent the bundle ships, named by its plist's FILE
// NAME — "io.example.thing.plist", not a path and not a label. The file must
// be at Contents/Library/LaunchAgents inside the bundle;
// +[SMAppService agentServiceWithPlistName:] resolves it there and nowhere
// else.
func Agent(plistName string) Service { return Service{kind: kindAgent, name: plistName} }

// Daemon is a system-wide LaunchDaemon the bundle ships, named by its plist's
// file name at Contents/Library/LaunchDaemons.
//
// Registering one asks for an administrator's password: a daemon runs as root,
// before anybody logs in. That prompt is macOS's, and it arrives whether or not
// the calling program expected it.
func Daemon(plistName string) Service { return Service{kind: kindDaemon, name: plistName} }

// LoginItem is a helper application inside this bundle, named by ITS bundle
// identifier: +[SMAppService loginItemServiceWithIdentifier:]. It is the
// modern replacement for SMLoginItemSetEnabled.
func LoginItem(identifier string) Service {
	return Service{kind: kindLoginItem, name: identifier}
}

// String describes the service, for a log or an error.
func (s Service) String() string {
	switch s.kind {
	case kindMainApp:
		return "the main application"
	case kindAgent:
		return "agent " + s.name
	case kindDaemon:
		return "daemon " + s.name
	case kindLoginItem:
		return "login item " + s.name
	}
	return fmt.Sprintf("service(kind %d, %q)", int(s.kind), s.name)
}

// Name is the plist file name or login-item identifier the service was made
// with, and "" for [MainApp].
func (s Service) Name() string { return s.name }

// SystemError is an NSError from SMAppServiceErrorDomain, with the operation
// that produced it.
//
// The message is macOS's own -localizedDescription, carried through unaltered
// rather than translated into something friendlier: it is the only text that
// says which of the several ways a registration can fail actually happened, and
// a friendlier summary would lose exactly that.
//
// Three that come up, from this package's own runs:
//
//   - code 3, "Codesigning failure loading plist" — the caller is not in a
//     bundle. This package reports [ErrNotBundled] before reaching that.
//   - code 108, "Unable to read plist: NAME" — there is a bundle, but no plist
//     of that name inside it.
//   - code 22, "Invalid argument" — unregistering something that was never
//     registered. Read [Service.Status] first if that is not an error to you.
type SystemError struct {
	// Op is "register" or "unregister".
	Op string
	// Service is the service the operation was for.
	Service string
	// Domain is the NSError domain, normally "SMAppServiceErrorDomain".
	Domain string
	// Code is the NSError code.
	Code int
	// Message is -localizedDescription, verbatim.
	Message string
}

// Error implements the error interface.
func (e *SystemError) Error() string {
	return fmt.Sprintf("servicemanagement: %s %s: %s (%s %d)",
		e.Op, e.Service, e.Message, e.Domain, e.Code)
}

// ---------------------------------------------------------------------------
// Seams. Each is one whole operation rather than one Objective-C call, so the
// darwin half can hold the factory object and the message to it inside a single
// autorelease pool on a single pinned thread. They are assigned in an init():
// on darwin to the real ServiceManagement calls, elsewhere to stubs. Tests swap
// them for fakes, which is what makes every branch below reachable on a runner
// with no macOS.
// ---------------------------------------------------------------------------

var (
	// loadErr is nil on darwin and [ErrUnsupported] elsewhere. It is checked
	// before anything else, so an off-darwin caller is told the platform is
	// wrong rather than that its macOS is too old.
	loadErr error
	// available reports whether the SMAppService class exists in this process.
	available func() bool
	// bundleID reports -[[NSBundle mainBundle] bundleIdentifier], empty when
	// this process is not in a bundle.
	bundleID func() string
	// doRegister, doUnregister and doStatus each perform one whole operation.
	doRegister   func(k kind, name string) error
	doUnregister func(k kind, name string) error
	doStatus     func(k kind, name string) (int, error)
)

// Bundled reports this process's bundle identifier, and whether it has one.
//
// It is the question SMAppService really asks, and the honest answer to "will
// any of this work" — a path that merely looks like a .app is not enough, since
// a bundle with no CFBundleIdentifier fails the same way a bare executable
// does. Off darwin it reports false, which is the truth there too.
func Bundled() (string, bool) {
	if loadErr != nil {
		return "", false
	}
	id := bundleID()
	return id, id != ""
}

// check is the guard every operation shares, in the order that produces the
// most useful complaint: wrong platform, then a name that cannot cross the
// bridge, then a macOS with no SMAppService, then no bundle to be identified
// by.
func (s Service) check() error {
	if loadErr != nil {
		return loadErr
	}
	if s.kind != kindMainApp {
		// The bridge to NSString is +stringWithUTF8String:, which stops at
		// the first NUL — a name carrying one would be silently SHORTENED
		// into some other service's name, or into none.
		if err := objc.ValidateName(s.name); err != nil {
			return fmt.Errorf("servicemanagement: %s: %w", s, err)
		}
	}
	if !available() {
		return ErrTooOld
	}
	if bundleID() == "" {
		return ErrNotBundled
	}
	return nil
}

// Register asks macOS to start this service at login.
//
// A nil error does NOT mean it will run. macOS may register the service and
// leave it switched off, waiting for the person to allow it; read [Status] and
// show [Status.Advice] when it comes back [RequiresApproval]. That is not a
// failure and must not be reported as one.
//
// Registering an already-registered service is not an error: it is how a
// program re-asserts a registration after an update.
func (s Service) Register() error {
	if err := s.check(); err != nil {
		return err
	}
	return doRegister(s.kind, s.name)
}

// Unregister takes the registration away.
//
// macOS reports unregistering something that was never registered as an error
// (code 22, "Invalid argument") rather than as the no-op it looks like. That is
// left as macOS reports it rather than swallowed here: a caller for whom "gone
// is gone" is the wanted outcome reads [Status] first, and a caller that
// swallowed it blindly would also swallow a real refusal.
func (s Service) Unregister() error {
	if err := s.check(); err != nil {
		return err
	}
	return doUnregister(s.kind, s.name)
}

// Status reports what macOS currently thinks of the service.
//
// It never invents a status from an error: a service that cannot be asked about
// yields an error and the zero [Status], not [NotFound]. Outside a bundle
// macOS itself answers NotFound for everything, and passing that on would be
// this package telling a caller its plist is missing when the real answer is
// that nobody asked.
func (s Service) Status() (Status, error) {
	if err := s.check(); err != nil {
		return 0, err
	}
	raw, err := doStatus(s.kind, s.name)
	if err != nil {
		return 0, err
	}
	return Status(raw), nil
}
