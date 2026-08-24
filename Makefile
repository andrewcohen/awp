# awp
#
# Everything here is a convenience over the plain go commands, except the ghostty
# target, which is a convenience over something nobody should have to remember.
#
#   make install    the ordinary build, the way it has always been installed
#   make ghostty    the same, plus the libghostty-vt pane emulator
#   make ghostty-test  the tagged suite `make test` cannot compile
#   make check      the five gates: format, lint, vet, test, build
#
# go is invoked through mise because it is not on PATH by itself here.

GO      := mise exec -- go
LINT    := mise exec -- golangci-lint
GOFMT   := mise exec -- gofmt

# libghostty-vt requires exactly this: build.zig.zon sets minimum_zig_version to
# 0.16.0. Invoked through mise rather than listed in mise.toml on purpose — the
# ghostty build is optional, and nobody who will never make one should have a Zig
# toolchain fetched on their behalf.
ZIG_VERSION  := 0.16.0
ZIG          := mise exec zig@$(ZIG_VERSION) -- zig

# The archive is cached rather than rebuilt: the source is 3.6MB to fetch and the
# build is not quick. Remove this directory to re-fetch a newer libghostty-vt.
GHOSTTY_CACHE := $(HOME)/.cache/awp/libghostty-vt
GHOSTTY_SRC   := $(GHOSTTY_CACHE)/src
GHOSTTY_OUT   := $(GHOSTTY_CACHE)/out
GHOSTTY_LIB   := $(GHOSTTY_OUT)/lib/libghostty-vt.a
GHOSTTY_URL   := https://github.com/ghostty-org/ghostty/releases/download/tip/libghostty-vt-source.tar.gz

# Two things in this tree arrive at the linker as prebuilt objects with their own
# macOS deployment target, and ld warns once per object when the link targets
# something older than they do. Neither is a real problem and both are loud
# enough to bury whatever else a build said:
#
#   libghostty-vt.a — Zig builds it at its own default (13.0), and it is what
#     `make ghostty` links. This is the one you hit if you only build zdeck.
#   gdeck's Wails — Cocoa sources with no target of their own, so they compile
#     against the installed SDK (26.0 here). Wails expects the build to state a
#     target, which is why its own generated gdeck/build/darwin/Taskfile.yml sets
#     this same pair of flags; only the repo-wide targets here were missing them.
#
# Both flags, not just the linker one: the warning is a mismatch between how the
# objects were compiled and how they are linked, so setting only the link target
# moves it rather than removing it. Sources we compile get retargeted by
# CGO_CFLAGS; a prebuilt archive cannot be, which is what the floor below is
# about.
#
# The number has a floor: at least the minos of every prebuilt object the link
# pulls in. libghostty-vt.a is the binding one — `otool -l $(GHOSTTY_LIB) | grep
# -A3 LC_BUILD_VERSION` says what Zig built it at, and a Zig upgrade that raises
# that default is what would bring the warning back, on `make ghostty` only.
#
# Empty off darwin — the flags are clang's and other toolchains reject them.
MACOS_MIN := 13.0
ifeq ($(shell uname -s),Darwin)
MACOS_CFLAGS := -mmacosx-version-min=$(MACOS_MIN)
# -O2 -g is go's own default CGO_CFLAGS. Setting the variable replaces that
# default rather than adding to it, so it has to be restated or every cgo shim in
# the tree quietly loses its optimization to a warning fix.
CGO_ENV      := CGO_CFLAGS="-O2 -g $(MACOS_CFLAGS)" CGO_LDFLAGS="$(MACOS_CFLAGS)"
# go hands CGO_LDFLAGS to both the cgo compile and the final link, so an archive
# named there arrives at ld twice and ld says so — once per `make ghostty`, about
# a duplicate it is already handling correctly. Only the ghostty target names an
# archive, so only it needs this.
DUP_LIB_QUIET := -Wl,-no_warn_duplicate_libraries
else
MACOS_CFLAGS :=
CGO_ENV      :=
DUP_LIB_QUIET :=
endif

.PHONY: install ghostty ghostty-lib ghostty-test check fmt lint vet test build clean-ghostty

install:
	$(CGO_ENV) $(GO) install ./...

# ghostty installs over the same binary install does, so the two are one keystroke
# apart in both directions. That matters: this build is the experiment, and going
# back to the thing it is being compared against must not be a research task.
ghostty: $(GHOSTTY_LIB)
	CGO_ENABLED=1 \
	CGO_CFLAGS="-O2 -g -I$(GHOSTTY_OUT)/include $(MACOS_CFLAGS)" \
	CGO_LDFLAGS="$(GHOSTTY_LIB) $(MACOS_CFLAGS) $(DUP_LIB_QUIET)" \
	$(GO) install -tags ghosttyvt ./...
	@echo
	@echo "installed with the libghostty-vt emulator, which is what a pane runs on."
	@echo "  awp zdeck        panes"
	@echo "  make install     the ordinary build: no emulator, so no panes"

# The tagged suite: everything that drives a real terminal is behind
# -tags ghosttyvt, so a plain `make test` compiles none of it. Run this when you
# touch internal/vterm. One target rather than a command to remember — the flags
# are the same three the ghostty build needs, and a wrong one skips the tests it
# was supposed to run rather than failing.
ghostty-test: $(GHOSTTY_LIB)
	CGO_ENABLED=1 \
	CGO_CFLAGS="-O2 -g -I$(GHOSTTY_OUT)/include $(MACOS_CFLAGS)" \
	CGO_LDFLAGS="$(GHOSTTY_LIB) $(MACOS_CFLAGS) $(DUP_LIB_QUIET)" \
	$(GO) test -tags ghosttyvt ./...

ghostty-lib: $(GHOSTTY_LIB)

# -Demit-xcframework=false is not optional on macOS: without it the library builds
# fine and then xcodebuild fails the install step, which reads as a failed build.
# -Dsimd=false keeps the C++ SIMD dependency out, which is why the archive links
# against nothing but libc.
$(GHOSTTY_LIB):
	@mkdir -p $(GHOSTTY_SRC)
	curl -fsSL $(GHOSTTY_URL) | tar -xz -C $(GHOSTTY_SRC) --strip-components=1
	cd $(GHOSTTY_SRC) && $(ZIG) build \
		-Demit-lib-vt=true \
		-Demit-xcframework=false \
		-Dsimd=false \
		-Doptimize=ReleaseFast \
		--prefix $(GHOSTTY_OUT)
	@test -f $(GHOSTTY_LIB) || { echo "the build produced no $(GHOSTTY_LIB)"; exit 1; }

clean-ghostty:
	rm -rf $(GHOSTTY_CACHE)

# The gates, each its own command so a failure names itself.
check: fmt lint vet test build

fmt:
	@out=$$($(GOFMT) -l .); test -z "$$out" || { echo "not gofmt-clean:"; echo "$$out"; exit 1; }

lint:
	$(LINT) run ./...

vet:
	$(CGO_ENV) $(GO) vet ./...

test:
	$(CGO_ENV) $(GO) test ./...

build:
	$(CGO_ENV) $(GO) build ./...
