# awp
#
# Everything here is a convenience over the plain go commands, except the ghostty
# target, which is a convenience over something nobody should have to remember.
#
#   make install    the ordinary build, the way it has always been installed
#   make ghostty    the same, plus the libghostty-vt pane emulator
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

.PHONY: install ghostty ghostty-lib check fmt lint vet test build clean-ghostty

install:
	$(GO) install ./...

# ghostty installs over the same binary install does, so the two are one keystroke
# apart in both directions. That matters: this build is the experiment, and going
# back to the thing it is being compared against must not be a research task.
ghostty: $(GHOSTTY_LIB)
	CGO_ENABLED=1 \
	CGO_CFLAGS="-I$(GHOSTTY_OUT)/include" \
	CGO_LDFLAGS="$(GHOSTTY_LIB)" \
	$(GO) install -tags ghosttyvt ./...
	@echo
	@echo "installed with the libghostty-vt emulator available."
	@echo "  AWP_PANE_VT=ghostty awp zdeck   panes on libghostty-vt"
	@echo "  awp zdeck                       panes on x/vt, the default"
	@echo "  make install                    back to the ordinary build"

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
	$(GO) vet ./...

test:
	$(GO) test ./...

build:
	$(GO) build ./...
