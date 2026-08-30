// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

// Package servicemanagement registers a macOS application, agent or daemon to
// start at login, through SMAppService — from pure Go, with CGO_ENABLED=0. It
// reaches the ServiceManagement framework via github.com/go-macos/objc, which
// reaches it through purego: no cgo, no Objective-C source file, no launchctl.
//
//	svc := servicemanagement.MainApp()
//	if err := svc.Register(); err != nil {
//		return err
//	}
//	st, err := svc.Status()
//	if err != nil {
//		return err
//	}
//	if st == servicemanagement.RequiresApproval {
//		fmt.Println(st.Advice()) // tell the person; do not fail
//	}
//
// # Why this rather than a plist
//
// The legacy way to start something at login is to write a plist into
// ~/Library/LaunchAgents and hope launchd reads it — which is what
// github.com/go-macos/launchagent does, and still does. Since macOS 13 that is
// no longer the supported path for an application: SMAppService is. It keeps
// the registration with the bundle that owns it, it appears in System Settings
// under the application's own name instead of as an anonymous label, and — the
// part a plist can never have — the person can switch it off there, and the
// program can find out that they did.
//
// # requiresApproval is a NORMAL outcome, not a failure
//
// [Service.Register] can return nil and leave the service in
// [RequiresApproval]. That is macOS saying: it is registered, it will not run
// yet, a person has to allow it in System Settings > General > Login Items &
// Extensions. It happens routinely — most visibly when the person has switched
// this very item off before, in which case the switch stays off until they turn
// it back on, whatever the program does.
//
// A program that treats that as success runs nothing and says nothing. A
// program that treats it as an error tells the person something is broken when
// nothing is. Both are wrong, so [Status] is returned as a value rather than
// flattened into an error, and [Status.Advice] carries the sentence to show.
//
// # It requires a bundle, and that is the first thing that goes wrong
//
// SMAppService is a bundle API: it identifies the caller by its bundle
// identifier, and a bare executable has none. Outside a bundle the class is
// still there, the factory methods still hand back objects, and -status still
// answers — with [NotFound], which is indistinguishable from a service that is
// genuinely absent. The failure only surfaces at -register:, as
// "Codesigning failure loading plist", which names neither the cause nor the
// fix.
//
// So every operation here checks first, with -[NSBundle bundleIdentifier], and
// reports [ErrNotBundled]: distinct from every other error, and answerable.
// [Bundled] exposes the same question directly. github.com/go-macos/appbundle
// builds the .app to answer it with, in pure Go, in the same build:
//
//	_, err := appbundle.Build(appbundle.Spec{
//		Dir: "dist", Name: "godl", Identifier: "io.github.go-downloader.godl",
//		Version: "0.1.0", Executable: "build/godl", Accessory: true,
//	})
//
// # Where an agent's plist lives
//
// [Agent] and [Daemon] take a plist FILE NAME, not a path and not a label. The
// file must be shipped inside the bundle — Contents/Library/LaunchAgents for an
// agent, Contents/Library/LaunchDaemons for a daemon — and the name is the leaf
// of it, extension included:
//
//	servicemanagement.Agent("io.github.go-downloader.godl.plist")
//
// A name that does not resolve to a file in there is not rejected: the object
// exists, its status is [NotFound], and -register: fails with "Unable to read
// plist". That is the one to look for when a registration that "worked" during
// development stops working once the plist stopped being copied into the
// bundle.
//
// # Threads
//
// Nothing here is AppKit, so nothing here is main-thread-only: SMAppService is
// backed by XPC and every call in this package is safe from any goroutine. Each
// operation runs inside its own autorelease pool on a pinned OS thread (see
// objc.AutoreleasePool), because the factory methods hand back autoreleased
// objects and a goroutine that migrates between creating one and messaging it
// drains the pool on a thread that never owned it.
//
// # Portability
//
// Every exported symbol exists on every platform, so a consumer cross-compiles
// without a build tag of its own; off darwin each operation reports
// [ErrUnsupported] and [Bundled] reports false. The portable half — service
// identity, name validation, status typing and advice — behaves identically
// everywhere and is tested to the last branch on runners with no macOS in
// sight.
package servicemanagement
