# go-macos/servicemanagement

[![ci](https://github.com/go-macos/servicemanagement/actions/workflows/ci.yml/badge.svg)](https://github.com/go-macos/servicemanagement/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-macos/servicemanagement.svg)](https://pkg.go.dev/github.com/go-macos/servicemanagement)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue.svg)](LICENSE)

**`SMAppService` from pure Go, `CGO_ENABLED=0`: register an application, an
agent or a daemon to start at login, and find out when the person has to approve
it.** No cgo, no `launchctl`, no Objective-C source file anywhere in the build —
it reaches the ServiceManagement framework through
[`go-macos/objc`](https://github.com/go-macos/objc), which reaches it through
[purego](https://github.com/ebitengine/purego).

```go
svc := servicemanagement.MainApp()

if err := svc.Register(); err != nil {
        return err
}

st, err := svc.Status()
if err != nil {
        return err
}
if st == servicemanagement.RequiresApproval {
        fmt.Println(st.Advice()) // say it; do not fail, and do not stay silent
}
```

That is the whole API:

| | |
|---|---|
| `MainApp() Service` | `+[SMAppService mainAppService]` — the application itself. |
| `Agent(plistName string) Service` | `+agentServiceWithPlistName:` — a per-user LaunchAgent shipped in the bundle. |
| `Daemon(plistName string) Service` | `+daemonServiceWithPlistName:` — a system LaunchDaemon shipped in the bundle. |
| `LoginItem(identifier string) Service` | `+loginItemServiceWithIdentifier:` — a helper app, replacing `SMLoginItemSetEnabled`. |
| `(Service) Register() error` | ask macOS to start it at login. |
| `(Service) Unregister() error` | take the registration away. |
| `(Service) Status() (Status, error)` | `-status`: `NotRegistered`, `Enabled`, `RequiresApproval`, `NotFound`. |
| `(Status) Registered() / Running() / Advice()` | what it means, and what to tell a person. |
| `Bundled() (string, bool)` | the bundle identifier this process has, if any. |

## `requiresApproval` is a normal outcome, not a failure

`Register` can return `nil` and leave the service in `RequiresApproval`. That is
macOS saying: **it is registered, it will not run yet, a person has to allow it
in System Settings → General → Login Items & Extensions.** It happens
routinely — most visibly when the person switched this very item off before, in
which case the switch stays off whatever the program does.

A program that treats that as success runs nothing and says nothing. A program
that treats it as an error tells the person something is broken when nothing is.
Both are wrong, which is why `Status` is a value rather than something flattened
into an error, and why `Status.Advice()` exists at all — it carries the sentence
to put in front of the person, naming the pane they would otherwise never find.

```go
switch st {
case servicemanagement.Enabled:
        // nothing to say
case servicemanagement.RequiresApproval, servicemanagement.NotRegistered, servicemanagement.NotFound:
        fmt.Fprintln(os.Stderr, st.Advice())
}
```

## It requires a bundle, and macOS hides that from you

`SMAppService` identifies its caller by **bundle identifier**. A bare executable
has none — and macOS does not say so. Outside a bundle:

- the class is there, and the factory methods hand back real objects;
- `-status` answers `notFound`, which is exactly what a genuinely missing
  service answers;
- only `-register:` fails, and it fails with **`Codesigning failure loading
  plist … code: -67028`** — which names neither the cause nor the fix.

So every operation here asks `-[[NSBundle mainBundle] bundleIdentifier]` first
and reports **`ErrNotBundled`**, which is a distinct sentinel from every other
error in the package. `Bundled()` asks the same question directly, which is what
a caller uses to *choose* between this package and a plist.

[`go-macos/appbundle`](https://github.com/go-macos/appbundle) builds the bundle,
in pure Go, in the same cross-compiling build:

```go
_, err := appbundle.Build(appbundle.Spec{
        Dir: "dist", Name: "godl", Identifier: "io.github.go-downloader.godl",
        Version: "0.1.0", Executable: "build/godl", Accessory: true,
})
```

## Where an agent's plist lives

`Agent` and `Daemon` take a plist **file name** — not a path, not a launchd
label:

```go
servicemanagement.Agent("io.github.go-downloader.godl.plist")
```

The file must be inside the bundle, at `Contents/Library/LaunchAgents`
(`Contents/Library/LaunchDaemons` for a daemon). A name that resolves to nothing
is not rejected: the object exists, its status is `NotFound`, and `-register:`
fails with `Unable to read plist` (code 108). That is the one to look for when a
registration that worked during development stops working the day the plist
stops being copied into the bundle.

## Against the legacy path

[`go-macos/launchagent`](https://github.com/go-macos/launchagent) writes a plist
into `~/Library/LaunchAgents`. That still works, and it is still the only thing
that works for a program that is not an application — a CLI in `/usr/local/bin`,
a build artefact, macOS 12. It is also invisible to the person: the item appears
in System Settings under a reverse-DNS label with no name and no icon, and
nothing tells the program when they switch it off.

`launchagent` prefers this package when the caller is in a bundle, and falls
back to the plist when it is not.

| | plist in `~/Library/LaunchAgents` | `SMAppService` |
|---|---|---|
| needs a bundle | no | **yes** |
| macOS | any | 13+ |
| shown to the person as | a bare label | the application, by name and icon |
| person can switch it off | not really | yes — and you can find out |
| supported by Apple | deprecated for apps | yes |

## Errors

| | |
|---|---|
| `ErrUnsupported` | not darwin. Every symbol exists everywhere so consumers cross-compile. |
| `ErrNotBundled` | this process has no bundle identifier. |
| `ErrTooOld` | no `SMAppService` class: macOS 12 or earlier. |
| `ErrNoService` | the factory yielded nil — not the same as a service being absent, which is the `NotFound` *status*. |
| `*SystemError` | an `NSError` from `SMAppServiceErrorDomain`, carrying `Op`, `Domain`, `Code` and macOS's own `Message`, verbatim. |

`Status` is never invented from an error. A service that could not be asked
about yields the zero `Status` and an error, because zero happens to be
`NotRegistered` and answering "not registered" to a question nobody could ask is
a lie a caller would act on.

## Threads

Nothing here is AppKit, so nothing here is main-thread-only: `SMAppService` is
XPC-backed and every call is safe from any goroutine. Each operation runs inside
its own autorelease pool on a pinned OS thread — the factory methods hand back
autoreleased objects, and a goroutine that migrates between creating one and
messaging it drains the pool on a thread that never owned it, which is a SIGSEGV
inside `objc_autoreleasePoolPop` with nothing of this package on the stack.

## Testing

The portable half — service identity, name validation, the guard order, status
typing and advice — is at **100%** on every platform, through seams the tests
point at fakes. The Objective-C half is covered by a live suite that calls the
real framework on a real Mac; it is at **99%**, the one uncovered statement
being the branch where `-register:` *succeeds*, which no test may reach because
reaching it means really adding a login item to the machine running the tests.

Every service the live suite names is one macOS cannot register, so it reads and
is refused, and leaves nothing behind.

BSD-3-Clause.
