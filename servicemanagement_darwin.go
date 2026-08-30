// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

//go:build darwin

package servicemanagement

import (
	"sync"
	"unsafe"

	"github.com/go-macos/objc"
)

// init points the seams declared in servicemanagement.go at the real
// ServiceManagement calls. Everything above them — service identity, name
// validation, the guard order, status typing and advice — is shared with every
// platform; only these leaf operations are darwin's.
func init() {
	loadErr = nil
	available = smAvailable
	bundleID = mainBundleIdentifier
	doRegister = registerService
	doUnregister = unregisterService
	doStatus = statusOfService
}

// frameworkOnce loads Foundation and ServiceManagement the first time anything
// here names a class.
//
// The load belongs here rather than with the caller. A caller who forgets gets
// a nil class, a nil service object, and every message after it returning zero
// — in silence, because Objective-C does not complain about a message to nil.
// The program then reports "not registered" forever and nothing in a log says
// why.
var frameworkOnce sync.Once

// load opens the frameworks, once per process.
func load() { frameworkOnce.Do(func() { _ = objc.Load(objc.Foundation, Framework) }) }

// smAvailable reports whether this macOS has SMAppService at all. The class is
// absent on macOS 12 and earlier, which is the only way this answers false.
func smAvailable() bool {
	load()
	return objc.ClassID("SMAppService") != 0
}

// mainBundleIdentifier is -[[NSBundle mainBundle] bundleIdentifier], and "" for
// a process that is not in a bundle — including one whose .app has no
// CFBundleIdentifier, which fails exactly like a bare executable and looks
// nothing like it.
//
// The read runs inside a pool because -bundleIdentifier hands back an
// autoreleased NSString and this is called from goroutines that have none.
// There is no nil check on the bundle: a message to nil in Objective-C answers
// nil, [objc.GoString] renders that as "", and "" is precisely the answer a
// process with no bundle should get. A guard here would be a branch no test
// could ever reach.
func mainBundleIdentifier() string {
	var id string
	objc.AutoreleasePool(func() {
		load()
		bundle := objc.ClassID("NSBundle").Send(objc.Sel("mainBundle"))
		id = objc.GoString(bundle.Send(objc.Sel("bundleIdentifier")))
	})
	return id
}

// serviceObject makes the SMAppService for one kind and name.
//
// The object is autoreleased, so it is only valid inside the pool its caller
// established — which is why each seam below opens one and does the whole
// operation inside it, rather than handing the object back across a seam
// boundary where a goroutine could migrate off the thread that owns the pool.
//
// A macOS with no SMAppService yields the zero class, and a message to it
// answers nil, so that case needs no branch of its own: it arrives here as the
// zero ID the callers already handle.
func serviceObject(k kind, name string) objc.ID {
	load()
	cls := objc.ClassID("SMAppService")
	switch k {
	case kindMainApp:
		return cls.Send(objc.Sel("mainAppService"))
	case kindAgent:
		return cls.Send(objc.Sel("agentServiceWithPlistName:"), objc.NSString(name))
	case kindDaemon:
		return cls.Send(objc.Sel("daemonServiceWithPlistName:"), objc.NSString(name))
	case kindLoginItem:
		return cls.Send(objc.Sel("loginItemServiceWithIdentifier:"), objc.NSString(name))
	}
	return 0
}

// registerService is -[SMAppService registerAndReturnError:].
func registerService(k kind, name string) error {
	return operate("register", k, name, objc.Sel("registerAndReturnError:"))
}

// unregisterService is -[SMAppService unregisterAndReturnError:].
func unregisterService(k kind, name string) error {
	return operate("unregister", k, name, objc.Sel("unregisterAndReturnError:"))
}

// operate sends one BOOL-returning, NSError**-taking message and turns its
// result into a Go error.
//
// The out-parameter is a Go variable whose address is handed over as an
// unsafe.Pointer: the collector tracks it, so no uintptr ever holds the only
// reference to the object macOS writes there. The NSError is autoreleased, so
// it is read here, inside the same pool.
func operate(op string, k kind, name string, sel objc.SEL) error {
	var err error
	objc.AutoreleasePool(func() {
		obj := serviceObject(k, name)
		if obj == 0 {
			err = ErrNoService
			return
		}
		var nsErr objc.ID
		if objc.Send[bool](obj, sel, unsafe.Pointer(&nsErr)) {
			return
		}
		err = systemError(op, k, name, nsErr)
	})
	return err
}

// statusOfService is -[SMAppService status], an NSInteger.
func statusOfService(k kind, name string) (int, error) {
	var (
		raw int
		err error
	)
	objc.AutoreleasePool(func() {
		obj := serviceObject(k, name)
		if obj == 0 {
			err = ErrNoService
			return
		}
		raw = objc.Send[int](obj, objc.Sel("status"))
	})
	return raw, err
}

// systemError renders the NSError an operation reported.
//
// A failed operation with a nil NSError is possible — the method contract only
// promises an error object when it returns NO, and nothing enforces it — so it
// is described rather than dereferenced into a message about nothing.
func systemError(op string, k kind, name string, nsErr objc.ID) error {
	e := &SystemError{Op: op, Service: Service{kind: k, name: name}.String()}
	if nsErr == 0 {
		e.Message = "the operation failed and macOS gave no reason"
		return e
	}
	e.Domain = objc.GoString(nsErr.Send(objc.Sel("domain")))
	e.Code = objc.Send[int](nsErr, objc.Sel("code"))
	e.Message = objc.GoString(nsErr.Send(objc.Sel("localizedDescription")))
	return e
}
