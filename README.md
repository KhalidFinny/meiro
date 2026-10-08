# Meiro Counter

A small native desktop counter built with [MyGo](https://mygo.egoist.dev/).

## Run

```sh
go tool mygo dev
```

Use `+` and `−` to change the count. The app is native Go UI; it does not need Bun or a web frontend.

## Test and build

```sh
go test ./...
go tool mygo build
```

The app needs Go 1.27.1 and GTK 3 on Linux. In an Amp orb, `.agents/setup` installs the pinned Go toolchain and GTK 3 runtime.
